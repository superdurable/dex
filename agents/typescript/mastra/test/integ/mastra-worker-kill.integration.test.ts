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
  createScriptedMastraAgentFlow,
  scriptedMastraMarkerDirectoryVariable,
} from "../fixtures/scripted-mastra-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/scripted-mastra-worker.js", import.meta.url));

describe("Mastra agent sessions survive a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createScriptedMastraAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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
      environment: { [scriptedMastraMarkerDirectoryVariable]: markerDirectory },
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
      `send_email -> email sent for ${sessionId}/turn-1/call-1-send_email`,
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "send_email.log")).names(), [
      `${sessionId} call-1-send_email`,
    ]);
  });

  test("a Worker killed during CallSingleAttemptTool records outcome_unknown and never reruns the tool", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `outcome-unknown-${randomUUID()}`;
    await sessions.startSession(sessionId, "call charge_card");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    assert.equal((await sessions.readAgentState(sessionId))?.status, "running_tools");
    assert.equal((await sessions.readToolCall(sessionId, "call-1-charge_card"))?.status, "approved");
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    // Releasing first means a wrongful rerun would finish and log a second invocation.
    chargeGate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `charge_card -> ${outcomeUnknownToolResult}`);
    assert.equal(snapshot.messages.at(-1)?.toolOutput, outcomeUnknownToolResult);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "charge_card.log")).names(), [
      `${sessionId} call-1-charge_card`,
    ]);
    assert.equal(chargeGate.reachedCount(), 1);
    const finishedStatuses = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
    assert.deepEqual(finishedStatuses, ["outcome_unknown"]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "RecordToolOutcomeUnknown",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a Worker killed during RunModel reruns only that model call, not the tool before it", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `model-rerun-${randomUUID()}`;
    await sessions.startSession(sessionId, "call get_weather then pause");
    const turnGate = new FileGate(markerDirectory, "turn");
    await turnGate.waitUntilReached();
    assert.equal((await sessions.readAgentState(sessionId))?.status, "running_model");

    await firstWorker.kill();
    turnGate.release();
    await startWorker(markerDirectory);

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, 'get_weather -> {"forecast":"sunny in Paris"}');
    assert.deepEqual(new InvocationLog(join(markerDirectory, "get_weather.log")).names(), [
      `${sessionId} call-1-get_weather`,
    ]);
    // The first model call ran once; only the killed second call ran again.
    const modelCalls = new InvocationLog(join(markerDirectory, "model-calls.log"));
    assert.equal(modelCalls.count("scripted-model call get_weather then pause"), 3);
    assert.equal(turnGate.reachedCount(), 2);
    assert.equal(snapshot.usage.modelCalls, 2);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a Worker killed during RunModel after streaming text retries the model call as a new attempt", async () => {
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
    assert.equal(new InvocationLog(join(markerDirectory, "turn.log")).count("slow stream"), 2);
    const events = await sessions.listAgentEvents(sessionId);
    const textByAttempt = new Map<number, string>();
    for (const event of events.filter((candidate) => candidate.type === "text_delta")) {
      const text = (JSON.parse(event.payload) as { text: string }).text;
      textByAttempt.set(event.attempt, (textByAttempt.get(event.attempt) ?? "") + text);
    }
    assert.deepEqual([...textByAttempt.entries()], [
      [1, "partial text "],
      [2, "partial text slow turn done"],
    ]);
    assert.deepEqual(
      events.filter((event) => event.type === "turn_completed").map((event) => event.attempt),
      [2],
    );
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "partial text \n\nslow turn done");
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
