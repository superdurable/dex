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
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
  waitForCompletedEvent,
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
      `send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email`,
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
    assert.equal(executingState?.status, "running_tools");
    assert.equal((await sessions.readToolCall(sessionId, "turn-1-charge_card"))?.status, "approved");
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

  test("a Worker killed before the first heartbeat reaches Dex runs a single-attempt tool once", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `kill-window-${randomUUID()}`;
    await sessions.startSession(sessionId, "call blocking_write");
    const gate = new FileGate(markerDirectory, "blocking_write");
    await gate.waitUntilReached();

    await firstWorker.kill();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `blocking_write -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "blocking_write.log")).names(), [
      `${sessionId} turn-1-blocking_write attempt 1`,
    ]);
    assert.equal(gate.reachedCount(), 1);
    const events = await sessions.listAgentEvents(sessionId);
    // This event precedes the heartbeat in the Worker's queue, so neither reached Dex.
    assert.equal(events.some((event) => event.type === "tool_call_started"), false);
    assert.deepEqual(await finishedStatuses(sessionId), ["outcome_unknown"]);
  });

  test("a Worker killed before the first heartbeat reaches Dex retries an idempotent write", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `kill-window-retry-${randomUUID()}`;
    await sessions.startSession(sessionId, "call blocking_refund");
    await new FileGate(markerDirectory, "blocking_refund").waitUntilReached();

    await firstWorker.kill();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `blocking_refund -> blocking_refund done for ${sessionId}/turn-1/turn-1-blocking_refund`,
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "blocking_refund.log")).names(), [
      `${sessionId} turn-1-blocking_refund attempt 1`,
      `${sessionId} turn-1-blocking_refund attempt 2`,
    ]);
  });

  test("a Worker killed before any model output reruns the model call", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `kill-window-model-${randomUUID()}`;
    await sessions.startSession(sessionId, "block");
    const gate = new FileGate(markerDirectory, "turn");
    await gate.waitUntilReached();

    await firstWorker.kill();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "echo: block");
    assert.equal(gate.reachedCount(), 2);
    assert.deepEqual(
      (await readStepHistory(engine.serverAddress, sessionId))
        .filter((entry) => entry.stepType === "RunModel")
        .map((entry) => entry.finalAttempt),
      [2],
    );
  });

  test("a Worker killed while a session is idle leaves the next message to the next Worker", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `kill-idle-${randomUUID()}`;
    await sessions.startSession(sessionId, "first");
    await sessions.waitForCompletedTurns(sessionId, 1);

    await firstWorker.kill();
    await startWorker(markerDirectory);

    assert.equal(await sessions.sendUserMessage(sessionId, "second"), true);
    await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "echo: second");
    assert.deepEqual(
      (await readStepHistory(engine.serverAddress, sessionId))
        .filter((entry) => entry.stepType === "RunModel" && entry.eventType === executeCompletedEvent)
        .map((entry) => entry.finalAttempt),
      [1, 1],
    );
  });

  for (const { tool, gateName, status } of [
    { tool: "lookup_a", gateName: "lookups", status: "failed" },
    { tool: "transfer_funds", gateName: "transfer_funds", status: "outcome_unknown" },
  ]) {
    test(`a Worker killed on every attempt of ${tool} records ${status}`, async () => {
      const markerDirectory = newMarkerDirectory();
      let worker = await startWorker(markerDirectory);
      const sessionId = `kill-every-${randomUUID()}`;
      await sessions.startSession(sessionId, `call ${tool}`);
      const gate = new FileGate(markerDirectory, gateName);

      for (const attempt of [1, 2, 3]) {
        await pollUntil(() => (gate.reachedCount() >= attempt ? true : undefined), {
          description: `attempt ${attempt} of ${tool} at its gate`,
        });
        await worker.kill();
        worker = await startWorker(markerDirectory);
      }
      await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

      assert.equal(new InvocationLog(join(markerDirectory, `${tool}.log`)).names().length, 3);
      assert.deepEqual(await finishedStatuses(sessionId), [status]);
    });
  }

  test("approval then a single-attempt write killed under an ASYNC step durability FlowConfig never reruns", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `async-durability-${randomUUID()}`;
    await sessions.startSession(sessionId, "call charge_card", {
      configOverride: { stepDurability: "async" },
    });
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    chargeGate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `charge_card -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "charge_card.log")).names(), [
      `${sessionId} turn-1-charge_card attempt 1`,
    ]);
    assert.equal(chargeGate.reachedCount(), 1);
  });

  test("a Worker killed during CallTool retries the idempotent write with the same key", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `idempotent-retry-${randomUUID()}`;
    await sessions.startSession(sessionId, "call transfer_funds");
    const transferGate = new FileGate(markerDirectory, "transfer_funds");
    await transferGate.waitUntilReached();
    await waitForEvent(sessionId, "tool_call_started");

    await firstWorker.kill();
    transferGate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    const key = `${sessionId}/turn-1/turn-1-transfer_funds`;
    assert.deepEqual(new InvocationLog(join(markerDirectory, "transfer_funds-keys.log")).names(), [
      `${key} attempt 1`,
      `${key} attempt 2`,
    ]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "transfer_funds -> funds transferred");
  });

  test("a Worker killed during RunModel reruns only that model call, not the tools before it", async () => {
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
    // A kill fails the attempt, so the rerun is attempt 2; the gate count already shows the rerun.
    assert.equal(turnGate.reachedCount(), 2);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "get_weather.log")).names(), [
      `${sessionId} turn-1-get_weather attempt 1`,
    ]);
    assert.equal(snapshot.usage.modelCalls, 2);
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

  test("a single-attempt tool killed while it runs again after an answer records outcome_unknown once", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `tool-input-kill-${randomUUID()}`;
    await sessions.startSession(sessionId, "call confirm_shipment");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "confirm_shipment");
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "yes"), true);
    const confirmGate = new FileGate(markerDirectory, "confirm_shipment");
    await confirmGate.waitUntilReached();

    await firstWorker.kill();
    confirmGate.release();
    await startWorker(markerDirectory);

    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `confirm_shipment -> ${outcomeUnknownToolResult}`,
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "confirm_shipment.log")).names(), [
      `${sessionId} turn-1-confirm_shipment attempt 1`,
      `${sessionId} turn-1-confirm_shipment attempt 1`,
    ]);
    assert.equal(confirmGate.reachedCount(), 1);
  });

  test("a Worker killed while a task waits for its next poll leaves the polling to the next Worker", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `task-wait-kill-${randomUUID()}`;
    await sessions.startSession(sessionId, "call gated_report_task");
    const gate = new FileGate(markerDirectory, "gated_report_task");
    await gate.waitUntilReached();
    // The second PollToolTask waits on its 3 second Timer when the Worker dies.
    await waitForStepMethods(sessionId, "PollToolTask", waitForCompletedEvent, 2);

    await firstWorker.kill();
    gate.release();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "gated_report_task -> report ready");
    assert.deepEqual(taskRuns(markerDirectory, sessionId, " create "), [`${sessionId} create turn-1-gated_report_task`]);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "gated_report_task.log")).names(), [
      `${sessionId} turn-1-gated_report_task attempt 1`,
    ]);
    assert.deepEqual(await finishedStatuses(sessionId), ["succeeded"]);
  });

  test("a Worker killed during a poll polls again and never starts a second task", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `task-poll-kill-${randomUUID()}`;
    await sessions.startSession(sessionId, "call blocking_report_task");
    const gate = new FileGate(markerDirectory, "blocking_report_task");
    await gate.waitUntilReached();

    await firstWorker.kill();
    gate.release();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "blocking_report_task -> report ready",
    );
    assert.equal(gate.reachedCount(), 2);
    assert.equal(taskRuns(markerDirectory, sessionId, " create ").length, 1);
    assert.equal(new InvocationLog(join(markerDirectory, "blocking_report_task.log")).names().length, 1);
    assert.deepEqual(await finishedStatuses(sessionId), ["succeeded"]);
  });

  test("a retried tool attempt resumes the task its first attempt started", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = `task-start-kill-${randomUUID()}`;
    await sessions.startSession(sessionId, "call start_gated_report_task");
    const gate = new FileGate(markerDirectory, "start_gated_report_task");
    // The first attempt created the task and saved its ID as the heartbeat checkpoint.
    await gate.waitUntilReached();

    await firstWorker.kill();
    gate.release();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "start_gated_report_task -> report ready",
    );
    assert.deepEqual(new InvocationLog(join(markerDirectory, "start_gated_report_task.log")).names(), [
      `${sessionId} turn-1-start_gated_report_task attempt 1`,
      `${sessionId} turn-1-start_gated_report_task attempt 2`,
    ]);
    assert.equal(taskRuns(markerDirectory, sessionId, " create ").length, 1);
    assert.deepEqual(
      (await readStepHistory(engine.serverAddress, sessionId))
        .filter((entry) => entry.stepType === "CallTool")
        .map((entry) => entry.finalAttempt),
      [2],
    );
  });

  function taskRuns(markerDirectory: string, sessionId: string, kind: string): string[] {
    return new InvocationLog(join(markerDirectory, "task-runs.log"))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `) && line.includes(kind));
  }

  async function finishedStatuses(sessionId: string): Promise<unknown[]> {
    return (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: unknown }).status);
  }

  async function waitForStepMethods(sessionId: string, stepType: string, eventType: string, count: number) {
    await pollUntil(
      async () =>
        (await readStepHistory(engine.serverAddress, sessionId)).filter(
          (entry) => entry.stepType === stepType && entry.eventType === eventType,
        ).length >= count
          ? true
          : undefined,
      { description: `${count} ${stepType} ${eventType} in session ${sessionId}` },
    );
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
