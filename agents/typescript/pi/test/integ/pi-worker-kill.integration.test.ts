// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import { outcomeUnknownToolResult } from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
} from "@superdurable/dex-agents/testing";

import { FakeModelServer } from "../fixtures/fake-model-server.js";
import {
  createPiTestAgentFlow,
  piTestMarkerDirectoryVariable,
  piTestModelServerVariable,
  piWorkerKillFlowType,
} from "../fixtures/pi-test-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/pi-test-agent-worker.js", import.meta.url));

describe("Pi agent sessions survive a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let modelServer: FakeModelServer;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    modelServer = await FakeModelServer.start();
    const flow = createPiTestAgentFlow({
      flowType: piWorkerKillFlowType,
      modelServerBaseUrl: modelServer.baseUrl,
      markerDirectory: join(engine.stateDirectory, "client-unused-markers"),
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
  });

  after(async () => {
    await environment?.close();
    await modelServer?.close();
    await engine?.stop();
  });

  async function startWorker(markerDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, `worker-${workers.length}.log`),
      environment: {
        [piTestModelServerVariable]: modelServer.baseUrl,
        [piTestMarkerDirectoryVariable]: markerDirectory,
      },
    });
    workers.push(worker);
    return worker;
  }

  test("a non_idempotent_write Pi tool killed while it runs records outcome_unknown and never runs again", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const token = newToken();
    const sessionId = `pi-kill-write-${token}`;
    const gateName = `gated-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_email", name: "send_email", arguments: JSON.stringify({ to: gateName }) }] }
        : { text: "The email may or may not have been sent." },
    );
    const firstWorker = await startWorker(markerDirectory);
    await sessions.startSession(sessionId, `email ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const gate = new FileGate(markerDirectory, gateName);
    await gate.waitUntilReached();
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    // Releasing first means a wrongful rerun would finish and log a second run.
    gate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.find((message) => message.role === "tool")?.toolOutput, outcomeUnknownToolResult);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "send_email.log")).names(), [`${gateName} call_email`]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "RecordToolOutcomeUnknown",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a Worker killed while the model streams reruns that model call on the next Worker", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const token = newToken();
    const sessionId = `pi-kill-model-${token}`;
    let releaseFirstStream: () => void = () => undefined;
    const firstStreamHeld = new Promise<void>((resolve) => {
      releaseFirstStream = resolve;
    });
    modelServer.script(token, (_request, index) =>
      index === 0 ? { text: "partial ", pauseAfterText: () => firstStreamHeld } : { text: "complete answer" },
    );
    try {
      const firstWorker = await startWorker(markerDirectory);
      await sessions.startSession(sessionId, `stream ${token}`);
      await waitForEvent(sessionId, "text_delta");
      await firstWorker.kill();
      await startWorker(markerDirectory);
      const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
      assert.equal(state.lastTurnError, null);
    } finally {
      releaseFirstStream();
    }

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    assert.equal(requests[0]?.isAbortedByClient, true);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.role} ${message.text}`),
      [`user stream ${token}`, "assistant complete answer"],
    );
    assert.equal(snapshot.usage.modelCalls, 1);
  });

  test("a pi-durable tool with replay safe reruns after a Worker kill, and one without replay runs once", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const token = newToken();
    const sessionId = `pi-kill-durable-${token}`;
    const forecastGate = `gated-forecast-${token}`;
    const chargeGate = `gated-charge-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? {
            toolCalls: [
              { id: "call_forecast", name: "durable_forecast", arguments: JSON.stringify({ city: forecastGate }) },
              { id: "call_charge", name: "durable_charge", arguments: JSON.stringify({ amount: chargeGate }) },
            ],
          }
        : { text: "Forecast ready; the charge is unknown." },
    );
    const firstWorker = await startWorker(markerDirectory);
    await sessions.startSession(sessionId, `durable ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "durable_charge");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await new FileGate(markerDirectory, forecastGate).waitUntilReached();
    await new FileGate(markerDirectory, chargeGate).waitUntilReached();

    await firstWorker.kill();
    new FileGate(markerDirectory, forecastGate).release();
    new FileGate(markerDirectory, chargeGate).release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const outputs = new Map(
      (await sessions.getAgentSnapshot(sessionId)).messages
        .filter((message) => message.role === "tool")
        .map((message) => [message.toolName, message.toolOutput]),
    );
    assert.equal(outputs.get("durable_forecast"), `forecast for ${forecastGate}: rain`);
    assert.equal(outputs.get("durable_charge"), outcomeUnknownToolResult);
    assert.equal(new InvocationLog(join(markerDirectory, "durable_forecast.log")).names().length, 2);
    assert.equal(new InvocationLog(join(markerDirectory, "durable_charge.log")).names().length, 1);
  });

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  async function waitForEvent(sessionId: string, eventType: string): Promise<void> {
    await pollUntil(
      async () =>
        (await sessions.listAgentEvents(sessionId)).some((event) => event.type === eventType) ? true : undefined,
      { description: `${eventType} event in session ${sessionId}` },
    );
  }
});

function newToken(): string {
  return randomUUID().slice(0, 8);
}
