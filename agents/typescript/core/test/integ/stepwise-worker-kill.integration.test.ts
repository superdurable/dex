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
import { setTimeout as delay } from "node:timers/promises";
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
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
} from "../../src/testing/index.js";
import { readLoggedModelCalls } from "../fixtures/stepwise-scripted-language-model.js";
import {
  createStepwiseTestAgentFlow,
  stepwiseMarkerDirectoryVariable,
  builtinLoopTurnContinuationHookVariable,
} from "../fixtures/stepwise-test-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/stepwise-test-agent-worker.js", import.meta.url));

describe("Stepwise agent sessions survive a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const flow = createStepwiseTestAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
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

  async function startWorker(
    markerDirectory: string,
    options: { readonly hasTurnContinuationHook?: boolean } = {},
  ): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: {
        [stepwiseMarkerDirectoryVariable]: markerDirectory,
        ...(options.hasTurnContinuationHook === true ? { [builtinLoopTurnContinuationHookVariable]: "1" } : {}),
      },
    });
    workers.push(worker);
    return worker;
  }

  test("a Worker killed during the second RunModel of a continued turn reruns only that model call", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory, { hasTurnContinuationHook: true });
    const token = newToken();
    const sessionId = `stepwise-kill-continue-${token}`;
    await sessions.startSession(sessionId, `revise ${token} then pause`);
    const modelGate = new FileGate(markerDirectory, "model");
    await modelGate.waitUntilReached();
    const continued = await sessions.getAgentSnapshot(sessionId);
    assert.equal(continued.state?.status, "running_model");
    assert.deepEqual(
      continued.messages.map((message) => `${message.sequence} ${message.role} ${message.text}`),
      [
        `1 user revise ${token} then pause`,
        `2 assistant draft for ${token} then pause`,
        `3 user finalize ${token} then pause`,
      ],
    );

    await firstWorker.kill();
    modelGate.release();
    await startWorker(markerDirectory, { hasTurnContinuationHook: true });

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.sequence} ${message.role} ${message.text}`),
      [
        `1 user revise ${token} then pause`,
        `2 assistant draft for ${token} then pause`,
        `3 user finalize ${token} then pause`,
        `4 assistant partial final for ${token}. final for ${token}`,
      ],
    );
    assert.equal(modelGate.reachedCount(), 2);
    // The draft call ran once; only the killed second call ran again.
    const loggedCalls = readLoggedModelCalls(markerDirectory, token);
    assert.equal(loggedCalls.length, 3);
    // Through the AI SDK, the continued call's prompt ends with the message as a user message.
    assert.deepEqual(
      loggedCalls.slice(1).map((call) => call.prompt.at(-1)?.role),
      ["user", "user"],
    );
    assert.equal(snapshot.usage.modelCalls, 2);
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel", "RunModel"]);
  });

  test("a session parked by a Worker without the hook continues on a Worker with it", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const token = newToken();
    const sessionId = `stepwise-hook-upgrade-${token}`;
    // The tool waits at the gate, so the run state and the record are persisted by the Worker without the hook.
    await sessions.startSession(sessionId, `lookup gated-end-after-tools-${token}`);
    const lookupGate = new FileGate(markerDirectory, "lookup");
    await lookupGate.waitUntilReached();
    await firstWorker.kill();
    lookupGate.release();
    await startWorker(markerDirectory, { hasTurnContinuationHook: true });

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.sequence} ${message.role} ${message.text ?? message.toolName}`),
      [`1 user lookup gated-end-after-tools-${token}`, "2 assistant lookup_order", "3 tool lookup_order"],
    );
    // The hook ended the turn after the tool, so no model call followed it.
    assert.equal(readLoggedModelCalls(markerDirectory, token).length, 1);
    assert.equal(snapshot.usage.modelCalls, 1);

    // The next turn runs normally, with the hook's continue.
    await sessions.sendUserMessage(sessionId, `revise next-${token}`);
    await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `final for next-${token}`,
    );
  });

  test("a Worker killed during RunModel reruns only that model call and stores each message once", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const token = newToken();
    const sessionId = `stepwise-kill-model-${token}`;
    await sessions.startSession(sessionId, `lookup ${token} then pause`);
    const modelGate = new FileGate(markerDirectory, "model");
    await modelGate.waitUntilReached();
    assert.equal((await sessions.readAgentState(sessionId))?.status, "running_model");

    await firstWorker.kill();
    modelGate.release();
    await startWorker(markerDirectory);

    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const lookupResult = `{"order_id": "${token}", "status": "shipped"}`;
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.sequence} ${message.role} ${message.text ?? message.toolName}`),
      [
        `1 user lookup ${token} then pause`,
        "2 assistant lookup_order",
        "3 tool lookup_order",
        `4 assistant partial answer for ${token}. lookup_order: ${lookupResult}`,
      ],
    );
    assert.equal(modelGate.reachedCount(), 2);
    assert.equal(readLoggedModelCalls(markerDirectory, token).length, 3);
    assert.equal(new InvocationLog(join(markerDirectory, "lookup_order.log")).names().length, 1);
    assert.equal(snapshot.usage.modelCalls, 2);
  });

  test("a Worker killed during a single-attempt write records outcome_unknown and never reruns it", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const token = newToken();
    const sessionId = `stepwise-kill-write-${token}`;
    await sessions.startSession(sessionId, `charge ${token}`);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    await waitForEvent(sessionId, "tool_call_started");
    assert.equal((await sessions.readToolCall(sessionId, "call-1-0-charge_card"))?.status, "approved");

    await firstWorker.kill();
    // Releasing first means a wrongful rerun would finish and log a second run.
    chargeGate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.find((message) => message.role === "tool")?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, `charge_card: ${outcomeUnknownToolResult}`);
    assert.equal(new InvocationLog(join(markerDirectory, "charge_card.log")).names().length, 1);
    assert.equal(chargeGate.reachedCount(), 1);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "RecordToolOutcomeUnknown",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a question survives a Worker restart and its answer resumes the turn", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const token = newToken();
    const sessionId = `stepwise-kill-question-${token}`;
    await sessions.startSession(sessionId, `ask ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "ask_user");

    await firstWorker.kill();
    await startWorker(markerDirectory);

    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "teal"), true);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      'answer: {"status":"answered","answer":"teal"}',
    );
    assert.equal(readLoggedModelCalls(markerDirectory, token).length, 2);
  });

  test("a Step that comes due after a Worker kill waits and runs as attempt 1", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const token = newToken();
    const sessionId = `stepwise-kill-queued-${token}`;
    await sessions.startSession(sessionId, `wait 3 ${token}`);
    await sessions.waitForToolCall(sessionId, "call-1-0-durable_wait", (toolCall) => toolCall.status === "waiting_for_timer");

    await firstWorker.kill();
    // The Timer fires with no Worker, so the Step that follows it stays queued.
    await delay(4_000);
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.deepEqual(
      (await readStepHistory(engine.serverAddress, sessionId))
        .filter((entry) => entry.stepType === "AwaitTimer" && entry.eventType === executeCompletedEvent)
        .map((entry) => entry.finalAttempt),
      [1],
    );
  });

  test("a durable_wait Timer survives a Worker restart and fires once", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const token = newToken();
    const sessionId = `stepwise-kill-timer-${token}`;
    await sessions.startSession(sessionId, `wait 3 ${token}`);
    await sessions.waitForToolCall(sessionId, "call-1-0-durable_wait", (toolCall) => toolCall.status === "waiting_for_timer");

    await firstWorker.kill();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `waited: {"status":"completed","waited_seconds":3,"reason":"cool down ${token}"}`,
    );
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitTimer",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  function newMarkerDirectory(): string {
    return mkdtempSync(join(engine.stateDirectory, "markers-"));
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  async function waitForEvent(sessionId: string, eventType: string): Promise<void> {
    await pollUntil(
      async () => ((await sessions.listAgentEvents(sessionId)).some((event) => event.type === eventType) ? true : undefined),
      { description: `${eventType} event in session ${sessionId}` },
    );
  }
});

function newToken(): string {
  return randomUUID().slice(0, 8);
}
