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
  createScriptedVercelAiAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
} from "../fixtures/scripted-vercel-ai-agent.js";

const workerEntryModulePath = fileURLToPath(
  new URL("../fixtures/scripted-vercel-ai-agent-worker.js", import.meta.url),
);

describe("Vercel AI SDK agent sessions survive a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createScriptedVercelAiAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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

  test("a write waiting for approval survives a Worker restart, runs once after approval, and its result reaches the model", async () => {
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
      `send_email -> {"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"}`,
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "send_email.log")).names(), [
      `${sessionId} call-1-send_email attempt 1`,
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
    const toolResult = snapshot.messages.find((message) => message.role === "tool");
    assert.equal(toolResult?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, `charge_card -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "charge_card.log")).names(), [
      `${sessionId} call-1-charge_card attempt 1`,
    ]);
    assert.equal(chargeGate.reachedCount(), 1);
    const finishedStatuses = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
    assert.deepEqual(finishedStatuses, ["outcome_unknown"]);
    assert.ok((await executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
  });

  test("a Worker killed during RunModel reruns only that model call, not the model call or tool before it", async () => {
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
    assert.equal(snapshot.messages.at(-1)?.text, "get_weather -> sunny in Paris");
    // Dex re-delivers an attempt that produced no output, so the rerun may keep attempt 1.
    assert.equal(turnGate.reachedCount(), 2);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "get_weather.log")).names(), [
      `${sessionId} call-1-get_weather attempt 1`,
    ]);
    // One model call asked for the tool; the second ran twice because the first try was killed.
    assert.equal(new InvocationLog(join(markerDirectory, "model-calls.log")).names().length, 3);
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
    assert.deepEqual(new InvocationLog(join(markerDirectory, "turn.log")).names(), [
      `${sessionId} turn-1 attempt 1`,
      `${sessionId} turn-1 attempt 2`,
    ]);
    const events = await sessions.listAgentEvents(sessionId);
    const textDeltas = events.filter((event) => event.type === "text_delta");
    assert.deepEqual(
      textDeltas.map((event) => [event.attempt, (JSON.parse(event.payload) as { text: string }).text]),
      [
        [1, "partial text of attempt 1. "],
        [2, "partial text of attempt 2. slow turn done"],
      ],
    );
    // A UI keeps only the highest attempt within one Step execution, so both attempts must share it.
    const textStepExecutionIds = new Set(textDeltas.map((event) => event.stepExecutionId));
    assert.equal(textStepExecutionIds.size, 1);
    assert.match([...textStepExecutionIds][0] ?? "", /RunModel/);
    assert.deepEqual(
      events.filter((event) => event.type === "turn_completed").map((event) => event.attempt),
      [2],
    );
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "partial text of attempt 2. slow turn done");
    assert.equal(snapshot.usage.modelCalls, 1);
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

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }
});
