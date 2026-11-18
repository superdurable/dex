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

import {
  createScriptedOpenAIAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
  scriptedAgentRenamedToolsVariable,
} from "../fixtures/scripted-openai-agent.js";

const workerEntryModulePath = fileURLToPath(
  new URL("../fixtures/scripted-openai-agent-worker.js", import.meta.url),
);

describe("A Worker restarts on a deploy that renamed a tool the open turn already called", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createScriptedOpenAIAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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
    await engine?.stop();
  });

  async function startWorker(markerDirectory: string, renamedTools = ""): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, `worker-${workers.length + 1}.log`),
      environment: {
        [scriptedAgentMarkerDirectoryVariable]: markerDirectory,
        [scriptedAgentRenamedToolsVariable]: renamedTools,
      },
    });
    workers.push(worker);
    return worker;
  }

  function newMarkerDirectory(): string {
    return mkdtempSync(join(engine.stateDirectory, "markers-"));
  }

  test("a renamed tool whose call completed stays history and the turn finishes", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `renamed-read-${randomUUID()}`;
    await sessions.startSession(sessionId, "call get_weather then pause");
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();

    await firstWorker.kill();
    turnGate.release();
    await startWorker(markerDirectory, "get_weather=read_weather");

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "get_weather -> sunny in Paris");
    assert.deepEqual(
      snapshot.messages.filter((message) => message.role === "tool").map((message) => message.toolName),
      ["get_weather"],
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "get_weather.log")).names(), [
      `${sessionId} call_1_get_weather attempt 1`,
    ]);
    assert.equal(snapshot.usage.modelCalls, 2);
  });

  test("a renamed single-attempt tool killed in flight records outcome_unknown and the turn finishes", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `renamed-write-${randomUUID()}`;
    await sessions.startSession(sessionId, "call charge_card");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    chargeGate.release();
    await startWorker(markerDirectory, "charge_card=charge_payment");

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `charge_card -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "charge_card.log")).names(), [
      `${sessionId} call_1_charge_card attempt 1`,
    ]);
    assert.equal(chargeGate.reachedCount(), 1);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "RecordToolOutcomeUnknown",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("steering a turn rebuilt after a rename stores every call once", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `renamed-steer-${randomUUID()}`;
    await sessions.startSession(sessionId, "call charge_card then call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    chargeGate.release();
    await startWorker(markerDirectory, "charge_card=charge_payment");
    await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.steerUserMessage(sessionId, "history"), true);

    const state = await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.slice(1, 5).map((message) => `${message.role} ${message.toolName}`),
      ["assistant charge_card", "tool charge_card", "assistant send_email", "tool send_email"],
    );
    assert.equal(snapshot.messages[2]?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, "history | user: call charge_card then call send_email");
    assert.deepEqual(new InvocationLog(join(markerDirectory, "charge_card.log")).names(), [
      `${sessionId} call_1_charge_card attempt 1`,
    ]);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "send_email.log")).names(), []);
  });

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  async function waitForEvent(sessionId: string, eventType: string): Promise<void> {
    await pollUntil(
      async () =>
        (await sessions.listAgentEvents(sessionId)).some((event) => event.type === eventType)
          ? true
          : undefined,
      { description: `${eventType} event in session ${sessionId}` },
    );
  }
});
