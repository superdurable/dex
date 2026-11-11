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

import {
  buildPlanExecutionMessage,
  createAgentSessionFlow,
  noPlanNote,
  planningTurnInstruction,
  planningTurnToolRejection,
  writeTodosCalledMoreThanOnce,
  type AgentSnapshot,
} from "../../src/index.js";
import { BuiltinLoopAgentRunner } from "../../src/builtin-loop/index.js";
import { streamAgentUIMessageChunks, type AgentUIMessageChunk } from "../../src/ui-stream/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
  ScriptedModelClient,
  executeCompletedEvent,
  readStepHistory,
} from "../../src/testing/index.js";
import { ScriptedAgentRunner } from "../fixtures/scripted-agent-runner.js";
import {
  approvedPlanPrefix,
  createPlanAgentWorkerFlows,
  planAgentMarkerDirectoryVariable,
  planAgentUserMessageGateVariable,
  readPlanModelRequests,
  userMessageGateName,
  type LoggedPlanModelRequest,
} from "../fixtures/stepwise-plan-agent.js";

const workerEntryModulePath = fileURLToPath(new URL("../fixtures/stepwise-plan-agent-worker.js", import.meta.url));
const failed = "The tool call failed: ";

describe("plans: write_todos, plan mode, and execute_plan", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let writerSessions: AgentSessionTestClient;
  let requiredSessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    const [planFlow, writerFlow, requiredFlow] = createPlanAgentWorkerFlows(
      join(engine.stateDirectory, "client-unused-markers"),
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [planFlow, writerFlow, requiredFlow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, planFlow);
    writerSessions = new AgentSessionTestClient(environment.client, writerFlow);
    requiredSessions = new AgentSessionTestClient(environment.client, requiredFlow);
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

  async function startWorker(markerDirectory: string, userMessageGate?: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, `worker-${workers.length + 1}.log`),
      environment: {
        [planAgentMarkerDirectoryVariable]: markerDirectory,
        ...(userMessageGate === undefined ? {} : { [planAgentUserMessageGateVariable]: userMessageGate }),
      },
    });
    workers.push(worker);
    return worker;
  }

  function newMarkerDirectory(): string {
    return mkdtempSync(join(engine.stateDirectory, "plan-markers-"));
  }

  async function sendAndWait(sessionId: string, text: string, turnCount: number, planMode = false): Promise<AgentSnapshot> {
    assert.equal(await sessions.sendUserMessage(sessionId, text, planMode), true);
    await sessions.waitForCompletedTurns(sessionId, turnCount);
    return sessions.getAgentSnapshot(sessionId);
  }

  async function startWithDraft(text: string): Promise<string> {
    const sessionId = `plan-${randomUUID()}`;
    await sessions.startSession(sessionId);
    const snapshot = await sendAndWait(sessionId, text, 1, true);
    assert.equal(snapshot.plan?.status, "draft");
    return sessionId;
  }

  async function eventPayloads(sessionId: string, eventType: string): Promise<Record<string, unknown>[]> {
    return (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === eventType)
      .map((event) => JSON.parse(event.payload) as Record<string, unknown>);
  }

  test("write_todos replaces the plan and each call of a double write fails", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-${randomUUID()}`;
    const first = [
      { content: "Look up the order", status: "in_progress" },
      { content: "Refund it", status: "pending" },
    ];
    await sessions.startSession(sessionId, `todos ${JSON.stringify(first)}`);
    await sessions.waitForCompletedTurns(sessionId, 1);
    let snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.deepEqual(snapshot.plan, {
      revision: 1,
      status: "active",
      tasks: [
        { content: "Look up the order", status: "in_progress" },
        { content: "Refund it", status: "pending" },
      ],
      updatedAt: snapshot.plan?.updatedAt,
    });
    // A decoded state omits the `chat` mode.
    assert.equal(snapshot.state?.interactionMode, undefined);
    assert.equal(snapshot.messages.at(-1)?.text, 'write_todos: {"status":"updated","revision":1,"task_count":2}');
    // No tool Step runs: RunModel records the call and still moves to ResumeTurn.
    assert.deepEqual(await executedStepTypes(engine, sessionId), [
      "StartAgentSession",
      "RunModel",
      "ResumeTurn",
      "RunModel",
    ]);

    const done = first.map(({ content }) => ({ content, status: "completed" }));
    snapshot = await sendAndWait(sessionId, `todos ${JSON.stringify(done)}`, 2);
    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [2, "completed"]);
    assert.equal(
      modelRequests(markerDirectory, sessionId, "turn-2")[0]?.userTexts.at(-2),
      "[Dex] The current plan, revision 1, is active:\n- [in_progress] Look up the order\n- [pending] Refund it",
    );

    snapshot = await sendAndWait(sessionId, `todos-twice ${JSON.stringify(first)}`, 3);
    assert.deepEqual(turnToolOutputs(snapshot, "turn-3"), [
      failed + writeTodosCalledMoreThanOnce,
      failed + writeTodosCalledMoreThanOnce,
    ]);
    assert.equal(snapshot.plan?.revision, 2);

    const invalidArguments: [string, string][] = [
      ['{"todos": [{"content": " ", "status": "pending"}]}', "todos[0].content must be a non-empty string"],
      ['{"todos": [{"content": "a", "status": "done"}]}', "todos[0].status must be pending, in_progress, or completed"],
      ['{"todos": [{"content": "a", "status": "pending", "owner": "me"}]}', "todos[0] has unknown field owner"],
      ['{"todos": "a"}', "todos must be a list"],
      ['{"tasks": []}', "unknown argument tasks"],
      // NaN is not JSON, which the Python core holds too.
      ['{"todos": NaN}', "arguments must be a JSON object"],
      // Unknown names sort by code point: U+FFFF before U+1F600.
      ['{"todos": [], "\u{1f600}": 1, "\uffff": 2}', "unknown argument \uffff"],
    ];
    for (const [offset, [toolArguments, reason]] of invalidArguments.entries()) {
      const turn = 4 + offset;
      snapshot = await sendAndWait(sessionId, `todos-raw ${toolArguments}`, turn);
      assert.deepEqual(turnToolOutputs(snapshot, `turn-${turn}`), [`${failed}invalid todos: ${reason}`]);
    }
    assert.equal(snapshot.plan?.revision, 2);

    snapshot = await sendAndWait(sessionId, "todos []", 11);
    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status, snapshot.plan?.tasks], [3, "cleared", []]);
    assert.deepEqual(turnToolOutputs(snapshot, "turn-11"), ['{"status":"cleared","revision":3}']);
    assert.deepEqual(await eventPayloads(sessionId, "plan_updated"), [
      { revision: 1, status: "active", task_count: 2 },
      { revision: 2, status: "completed", task_count: 2 },
      { revision: 3, status: "cleared", task_count: 0 },
    ]);
  });

  test("a plan_mode message drafts with only write_todos, and execute_plan runs the current revision", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-${randomUUID()}`;
    await sessions.startSession(sessionId);
    let snapshot = await sendAndWait(sessionId, "plan Look up the order | Publish the note", 1, true);

    assert.equal(snapshot.state?.interactionMode, "planning");
    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [1, "draft"]);
    assert.deepEqual(
      snapshot.plan?.tasks.map((task) => task.status),
      ["pending", "pending"],
    );
    assert.equal(snapshot.messages.at(-1)?.text, "drafted 2 tasks");
    const [drafting, presenting] = modelRequests(markerDirectory, sessionId, "turn-1");
    // The default planning tool choice is auto: the model may also answer in text.
    assert.deepEqual([drafting?.tools, drafting?.toolChoice], [["write_todos"], "auto"]);
    assert.deepEqual(drafting?.userTexts.slice(-2), [
      `${noPlanNote}\n${planningTurnInstruction}`,
      "plan Look up the order | Publish the note",
    ]);
    assert.deepEqual([presenting?.tools, presenting?.toolChoice], [["write_todos"], "auto"]);
    assert.equal(
      presenting?.userTexts.at(-2),
      "[Dex] The current plan, revision 1, is draft:\n- [pending] Look up the order\n- [pending] Publish the note\n" +
        planningTurnInstruction,
    );

    // Feedback on the draft is a planning turn too, and may revise it without being forced.
    snapshot = await sendAndWait(sessionId, "revise Look up the order | Publish the note | finish up", 2);
    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [2, "draft"]);
    const [revising] = modelRequests(markerDirectory, sessionId, "turn-2");
    assert.deepEqual([revising?.tools, revising?.toolChoice], [["write_todos"], "auto"]);

    assert.equal(await sessions.executePlan(sessionId, 1), false);
    assert.equal(await sessions.executePlan(sessionId, 3), false);
    assert.equal(await sessions.executePlan(sessionId, 2), true);
    await sessions.waitForCompletedTurns(sessionId, 3);
    snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(snapshot.state?.interactionMode, "executing");
    const [executing] = modelRequests(markerDirectory, sessionId, "turn-3");
    assert.deepEqual(executing?.tools, ["lookup_order", "publish_note", "ask_user", "write_todos"]);
    assert.equal(executing?.toolChoice, "auto");
    assert.equal(executing?.userTexts.at(-1), buildPlanExecutionMessage(2));
    assert.equal(
      executing?.userTexts.at(-1),
      "[Dex] The user approved plan revision 2. Execute it and keep the task statuses current with write_todos.",
    );
    assert.ok(executing?.userTexts.at(-2)?.startsWith("[Dex] The current plan, revision 2, is active:"));
    const thirdTurnEvents = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.turnId === "turn-3")
      .map((event) => event.type);
    assert.deepEqual(thirdTurnEvents.slice(0, 3), ["turn_started", "plan_execution_started", "plan_updated"]);
    assert.deepEqual(await eventPayloads(sessionId, "plan_execution_started"), [{ revision: 2 }]);
    // The approval marked the draft active, then the execution completed every task.
    assert.deepEqual((await eventPayloads(sessionId, "plan_updated")).slice(-2), [
      { revision: 2, status: "active", task_count: 3 },
      { revision: 3, status: "completed", task_count: 3 },
    ]);
    assert.equal(invocationCount(markerDirectory, "lookup_order"), 1);
    // The execution marked every task completed, so nothing is left to execute.
    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [3, "completed"]);
    assert.equal(await sessions.executePlan(sessionId, 3), false);
  });

  test("a planning turn that answers in text ends without a draft", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-${randomUUID()}`;
    await sessions.startSession(sessionId);
    let snapshot = await sendAndWait(sessionId, "Which database do you use?", 1, true);

    assert.equal(snapshot.state?.interactionMode, "planning");
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.plan, undefined);
    assert.equal(snapshot.messages.at(-1)?.text, "said: Which database do you use?");
    assert.equal(await sessions.executePlan(sessionId, 1), false);
    snapshot = await sendAndWait(sessionId, "plan Look up the order", 2, true);
    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [1, "draft"]);
  });

  test("a required planning tool choice forces write_todos until a draft exists", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-required-${randomUUID()}`;
    await requiredSessions.startSession(sessionId);
    assert.equal(await requiredSessions.sendUserMessage(sessionId, "plan Look up the order", true), true);
    await requiredSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await requiredSessions.sendUserMessage(sessionId, "revise Look up the order | Refund it"), true);
    await requiredSessions.waitForCompletedTurns(sessionId, 2);

    const [drafting, presenting] = modelRequests(markerDirectory, sessionId, "turn-1");
    assert.deepEqual([drafting?.tools, drafting?.toolChoice], [["write_todos"], "write_todos"]);
    assert.deepEqual([presenting?.tools, presenting?.toolChoice], [["write_todos"], "auto"]);
    // A draft exists, so feedback on it is not forced.
    const [revising] = modelRequests(markerDirectory, sessionId, "turn-2");
    assert.deepEqual([revising?.tools, revising?.toolChoice], [["write_todos"], "auto"]);
  });

  test("a planning turn rejects every tool but write_todos", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-${randomUUID()}`;
    await sessions.startSession(sessionId);
    const snapshot = await sendAndWait(sessionId, "plan-sneak Look up the order", 1, true);

    assert.deepEqual(turnToolOutputs(snapshot, "turn-1"), [
      planningTurnToolRejection,
      '{"status":"updated","revision":1,"task_count":1}',
    ]);
    assert.equal(invocationCount(markerDirectory, "lookup_order"), 0);
    assert.deepEqual(
      (await eventPayloads(sessionId, "tool_call_finished")).map((payload) => [payload["tool_name"], payload["status"]]),
      [
        ["lookup_order", "rejected"],
        ["write_todos", "succeeded"],
      ],
    );
  });

  test("concurrent execute_plan calls for one revision accept exactly one", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-${randomUUID()}`;
    await sessions.startSession(sessionId);

    for (let revision = 1; revision <= 5; revision += 1) {
      const gateName = `execution-${revision}`;
      const snapshot = await sendAndWait(sessionId, `plan stall ${gateName}`, 2 * revision - 1, true);
      assert.equal(snapshot.plan?.revision, revision);
      const results = await Promise.all([
        sessions.executePlan(sessionId, revision),
        sessions.executePlan(sessionId, revision),
      ]);
      assert.deepEqual([...results].sort(), [false, true]);
      const gate = new FileGate(markerDirectory, gateName);
      await gate.waitUntilReached();
      assert.equal(await sessions.executePlan(sessionId, revision), false);
      gate.release();
      await sessions.waitForCompletedTurns(sessionId, 2 * revision);
    }

    assert.deepEqual(
      await eventPayloads(sessionId, "plan_execution_started"),
      [1, 2, 3, 4, 5].map((revision) => ({ revision })),
    );
    assert.equal(invocationCount(markerDirectory, "lookup_order"), 5);
  });

  test("a Worker killed while AwaitUserMessage starts an approved plan runs it once", async () => {
    const markerDirectory = newMarkerDirectory();
    const firstWorker = await startWorker(markerDirectory, `block:${approvedPlanPrefix}`);
    const sessionId = await startWithDraft("plan Look up the order");
    assert.equal(await sessions.executePlan(sessionId, 1), true);
    const gate = new FileGate(markerDirectory, userMessageGateName);
    await gate.waitUntilReached();

    await firstWorker.kill();
    gate.release();
    await startWorker(markerDirectory);
    await sessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.equal(snapshot.state?.interactionMode, "executing");
    assert.deepEqual(await eventPayloads(sessionId, "plan_execution_started"), [{ revision: 1 }]);
    assert.equal(invocationCount(markerDirectory, "lookup_order"), 1);
    assert.equal(modelRequests(markerDirectory, sessionId, "turn-2").length, 2);
    const executed = await executedStepTypes(engine, sessionId);
    assert.equal(executed.filter((stepType) => stepType === "AwaitUserMessage").length, 2);
    // The rerun cleared the pending approval, so the active plan can run again.
    assert.equal(snapshot.plan?.status, "active");
    assert.equal(await sessions.executePlan(sessionId, 1), true);
    await sessions.waitForCompletedTurns(sessionId, 3);
    assert.equal((await eventPayloads(sessionId, "plan_execution_started")).length, 2);
  });

  test("an approval the session took but has not started refuses a second one", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory, `fail:${approvedPlanPrefix}`);
    const sessionId = await startWithDraft("plan Look up the order");
    assert.equal(await sessions.executePlan(sessionId, 1), true);
    const gate = new FileGate(markerDirectory, userMessageGateName);
    await gate.waitUntilReached();

    // AwaitUserMessage consumed the approval, but its Execute has not committed.
    const idle = await sessions.getAgentSnapshot(sessionId);
    assert.equal(idle.state?.status, "waiting_for_user_message");
    assert.equal(idle.state?.activeTurnId, null);
    assert.equal(await sessions.executePlan(sessionId, 1), false);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);

    const snapshot = await sendAndWait(sessionId, "after the execution", 3);
    assert.deepEqual(
      snapshot.messages
        .filter((message) => message.role === "user" && message.turnId === "turn-3")
        .map((message) => message.text),
      ["after the execution"],
    );
    assert.deepEqual(await eventPayloads(sessionId, "plan_execution_started"), [{ revision: 1 }]);
    assert.equal(invocationCount(markerDirectory, "lookup_order"), 1);
  });

  test("an approval that a queued turn makes stale never runs", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory, "fail:revise ");
    const sessionId = await startWithDraft("plan Look up the order");
    assert.equal(await sessions.sendUserMessage(sessionId, "revise Look up the order | Publish the note"), true);
    const gate = new FileGate(markerDirectory, userMessageGateName);
    await gate.waitUntilReached();

    // The session took the message first, so this approval waits behind that message's turn.
    assert.equal(await sessions.executePlan(sessionId, 1), true);
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);

    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [2, "draft"]);
    assert.equal(await sessions.executePlan(sessionId, 1), false);
    assert.equal(await sessions.executePlan(sessionId, 2), true);
    await sessions.waitForCompletedTurns(sessionId, 3);
    assert.deepEqual(await eventPayloads(sessionId, "plan_execution_started"), [{ revision: 2 }]);
  });

  test("a runner without plan mode writes plans but refuses plan_mode messages", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = `plan-writer-${randomUUID()}`;
    await writerSessions.startSession(sessionId, "call write_todos");
    await writerSessions.waitForCompletedTurns(sessionId, 1);
    let snapshot = await writerSessions.getAgentSnapshot(sessionId);

    assert.deepEqual([snapshot.plan?.revision, snapshot.plan?.status], [1, "active"]);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      'write_todos -> {"status":"updated","revision":1,"task_count":1}',
    );
    assert.ok(!(await executedStepTypes(engine, sessionId)).includes("CallTool"));
    assert.equal(await writerSessions.sendUserMessage(sessionId, "plan it", true), false);
    assert.equal(await writerSessions.sendUserMessage(sessionId, "still chatting"), true);
    await writerSessions.waitForCompletedTurns(sessionId, 2);
    snapshot = await writerSessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "echo: still chatting");
    // A decoded state omits the `chat` mode.
    assert.equal(snapshot.state?.interactionMode, undefined);
  });

  test("the UI message stream carries plan events as event data and finishes the executing turn once", async () => {
    const markerDirectory = newMarkerDirectory();
    await startWorker(markerDirectory);
    const sessionId = await startWithDraft("plan stall ui-execution | finish up");
    assert.equal(await sessions.executePlan(sessionId, 1), true);
    const gate = new FileGate(markerDirectory, "ui-execution");
    await gate.waitUntilReached();
    const streaming = collectChunks(
      streamAgentUIMessageChunks({
        client: environment.client,
        flow: sessions.flow,
        flowId: sessionId,
        pollTimeoutMs: 1_000,
      }),
    );
    gate.release();
    const chunks = await streaming;

    assert.deepEqual(
      chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
      [{ type: "finish", finishReason: "stop" }],
    );
    assert.deepEqual(chunks.filter((chunk) => chunk.type === "tool-approval-request"), []);
    const planEvents = chunks.flatMap((chunk) => {
      const data = (chunk as { type: string; data?: { type?: string; payload?: string } }).data;
      return chunk.type === "data-agent-event" && data?.type?.startsWith("plan_") === true
        ? [[data.type, JSON.parse(data.payload ?? "{}") as unknown]]
        : [];
    });
    assert.deepEqual(planEvents, [
      ["plan_execution_started", { revision: 1 }],
      ["plan_updated", { revision: 1, status: "active", task_count: 2 }],
      ["plan_updated", { revision: 2, status: "completed", task_count: 2 }],
    ]);
    assert.deepEqual(
      chunks.flatMap((chunk) => (chunk.type === "tool-input-available" ? [chunk.toolName] : [])),
      ["lookup_order", "write_todos"],
    );
    assert.deepEqual(
      chunks.flatMap((chunk) => (chunk.type === "tool-output-available" ? [chunk.output] : [])),
      ["found stall ui-execution", '{"status":"updated","revision":2,"task_count":2}'],
    );
  });

  test("plan mode needs write_todos and a runner that supports it", () => {
    const markerDirectory = newMarkerDirectory();
    const model = new ScriptedModelClient(() => ({ text: "ok" }));
    assert.throws(
      () => createAgentSessionFlow(new ScriptedAgentRunner(markerDirectory), "NoWriteTodos", { planModeEnabled: true }),
      /planModeEnabled requires the write_todos built-in tool/,
    );
    assert.throws(
      () =>
        createAgentSessionFlow(new ScriptedAgentRunner(markerDirectory), "NoPlanMode", {
          builtInTools: ["write_todos"],
          planModeEnabled: true,
        }),
      /planModeEnabled requires a runner that supports plan mode/,
    );
    assert.throws(
      () => new BuiltinLoopAgentRunner({ name: "agent", instructions: "Help.", model, planModeEnabled: true }),
      /planModeEnabled requires the write_todos built-in tool/,
    );
    assert.throws(
      () =>
        new BuiltinLoopAgentRunner({
          name: "agent",
          instructions: "Help.",
          model,
          builtInTools: ["write_todos"],
          planningToolChoice: "none" as unknown as "auto",
        }),
      /planningToolChoice must be "auto" or "required"/,
    );
  });
});

function modelRequests(markerDirectory: string, sessionId: string, turnId: string): LoggedPlanModelRequest[] {
  return readPlanModelRequests(markerDirectory).filter(
    (request) => request.flowId === sessionId && request.turnId === turnId,
  );
}

function turnToolOutputs(snapshot: AgentSnapshot, turnId: string): (string | null)[] {
  return snapshot.messages
    .filter((message) => message.role === "tool" && message.turnId === turnId)
    .map((message) => message.toolOutput);
}

function invocationCount(markerDirectory: string, name: string): number {
  return new InvocationLog(join(markerDirectory, "invocations.log")).count(name);
}

async function executedStepTypes(engine: PrivateDexEngine, sessionId: string): Promise<string[]> {
  const history = await readStepHistory(engine.serverAddress, sessionId);
  return history.filter((entry) => entry.eventType === executeCompletedEvent).map((entry) => entry.stepType);
}

async function collectChunks(chunks: AsyncIterable<AgentUIMessageChunk>): Promise<AgentUIMessageChunk[]> {
  const collected: AgentUIMessageChunk[] = [];
  for await (const chunk of chunks) {
    collected.push(chunk);
  }
  return collected;
}
