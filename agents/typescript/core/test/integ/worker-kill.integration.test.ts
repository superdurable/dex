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

import { outcomeUnknownToolResult } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
  pollUntil,
} from "../../src/testing/index.js";
import {
  createScriptedAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
} from "../fixtures/scripted-agent-runner.js";

const workerEntryModulePath = fileURLToPath(
  new URL("../fixtures/scripted-agent-worker.js", import.meta.url),
);

describe("agent sessions survive a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createScriptedAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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

  async function startWorker(markerDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [scriptedAgentMarkerDirectoryVariable]: markerDirectory },
    });
    workers.push(worker);
    return worker;
  }

  function newMarkerDirectory(): string {
    return mkdtempSync(join(engine.stateDirectory, "markers-"));
  }

  test("a write waiting for approval survives a Worker restart and runs once after approval", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `approval-restart-${randomUUID()}`;
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");

    await firstWorker.kill();
    await startWorker(markerDirectory);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1-send_email`,
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "send_email.log")).names(), [
      `${sessionId} turn-1-send_email attempt 1`,
    ]);
  });

  test("a Worker killed during a single-attempt write records outcome_unknown and never reruns the tool", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `outcome-unknown-${randomUUID()}`;
    await sessions.startSession(sessionId, "call charge_card");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    const executingState = await sessions.readAgentState(sessionId);
    assert.equal(executingState?.status, "executing_tool");
    assert.equal(executingState?.executingToolCallId, "turn-1-charge_card");
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    // Releasing first means a wrongful rerun would finish and log a second invocation.
    chargeGate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const toolResult = snapshot.messages.find((message) => message.role === "tool");
    assert.equal(toolResult?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, `charge_card -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "charge_card.log")).names(), [
      `${sessionId} turn-1-charge_card attempt 1`,
    ]);
    assert.equal(chargeGate.reachedCount(), 1);
    const finishedEvents = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => JSON.parse(event.payload) as { status: string });
    assert.deepEqual(
      finishedEvents.map((payload) => payload.status),
      ["outcome_unknown"],
    );
  });

  test("a Worker killed during RunAgentTurn before any output reruns the turn segment", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `turn-rerun-${randomUUID()}`;
    await sessions.startSession(sessionId, "slow");
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();

    await firstWorker.kill();
    turnGate.release();
    await startWorker(markerDirectory);

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => message.text),
      ["slow", "slow turn done"],
    );
    // Dex re-delivers an attempt that produced no output, so the rerun may keep attempt 1.
    assert.equal(turnGate.reachedCount(), 2);
    assert.equal(snapshot.usage.modelCalls, 1);
  });

  test("a Worker killed during RunAgentTurn after streaming text retries the segment as a new attempt", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `turn-retry-${randomUUID()}`;
    await sessions.startSession(sessionId, "slow stream");
    await waitForEvent(sessionId, "text_delta");

    await firstWorker.kill();
    new FileGate(markerDirectory, "turn").release();
    await startWorker(markerDirectory);

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    assert.deepEqual(
      new InvocationLog(join(markerDirectory, "turn.log")).names().map(
        (line) => line.split(" attempt ")[1],
      ),
      ["1", "2"],
    );
    const events = await sessions.listAgentEvents(sessionId);
    const textByAttempt = events
      .filter((event) => event.type === "text_delta")
      .map((event) => [event.attempt, (JSON.parse(event.payload) as { text: string }).text]);
    assert.deepEqual(textByAttempt, [
      [1, "partial text of attempt 1"],
      [2, "partial text of attempt 2slow turn done"],
    ]);
    assert.deepEqual(
      events.filter((event) => event.type === "turn_completed").map((event) => event.attempt),
      [2],
    );
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "slow turn done");
  });

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
