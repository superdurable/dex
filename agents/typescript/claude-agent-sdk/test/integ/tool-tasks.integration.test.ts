// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Tool tasks and questions of a tool source; the twin of the Python adapter's tests/test_tool_tasks.py.

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import {
  outcomeUnknownToolResult,
  turnCancelledNoteHeader,
  type AgentToolCallRecord,
  type AgentToolCallRecordStatus,
} from "@superdurable/dex-agents";
import { PrivateDexEngine, pollUntil } from "@superdurable/dex-agents/testing";

import { ClaudeAgentRunner } from "../../src/index.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";

type ToolArguments = Readonly<Record<string, unknown>>;

function callReport(toolArguments: ToolArguments): string {
  return `call report ${JSON.stringify(toolArguments)}`;
}

describe("tool tasks and questions of a Claude agent's tool source", () => {
  let engine: PrivateDexEngine;
  let harness: ClaudeAgentTestHarness | undefined;

  before(async () => {
    engine = await PrivateDexEngine.start();
  });

  afterEach(async () => {
    await harness?.close();
    harness = undefined;
  });

  after(async () => {
    await engine?.stop();
  });

  async function startHarness(): Promise<ClaudeAgentTestHarness> {
    harness = await ClaudeAgentTestHarness.start(engine, true);
    return harness;
  }

  /** Returns the turn's only record while the final answer waits at the `held` gate. */
  async function heldRecord(agent: ClaudeAgentTestHarness, sessionId: string): Promise<AgentToolCallRecord> {
    const gate = agent.gate("held");
    await gate.waitUntilReached();
    const snapshot = await agent.sessions.getAgentSnapshot(sessionId);
    gate.release();
    assert.equal(snapshot.toolCalls.length, 1);
    return snapshot.toolCalls[0]!;
  }

  function waitForToolCallStatus(
    agent: ClaudeAgentTestHarness,
    sessionId: string,
    status: AgentToolCallRecordStatus,
  ): Promise<AgentToolCallRecord> {
    return pollUntil(
      async () => (await agent.sessions.getAgentSnapshot(sessionId)).toolCalls.find((record) => record.status === status),
      { description: `a ${status} tool call in session ${sessionId}`, timeoutMs: 120_000 },
    );
  }

  function taskInvocations(agent: ClaudeAgentTestHarness): string[] {
    return agent.invocations
      .names()
      .filter((name) => name.startsWith("start task-") || name.startsWith("poll task-") || name.startsWith("cancel-task "));
  }

  test("the runner polls and cancels tool tasks", () => {
    const runner = new ClaudeAgentRunner({});
    assert.equal(typeof runner.pollToolTask, "function");
    assert.equal(typeof runner.cancelToolTask, "function");
  });

  test("a source task is polled until its result reaches the resumed Claude session", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("task");
    await agent.sessions.startSession(sessionId, `${callReport({ topic: "q1", polls: 3 })} ;; pause held`);
    const record = await heldRecord(agent, sessionId);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "report: report on q1");
    assert.equal(record.status, "succeeded");
    assert.equal(record.output, "report on q1");
    const taskId = `task-${record.toolCallId}`;
    assert.equal(record.taskId, taskId);
    // Pending twice, then the result.
    assert.deepEqual(taskInvocations(agent), [
      `start ${taskId}`,
      `poll ${taskId} 1`,
      `poll ${taskId} 2`,
      `poll ${taskId} 3`,
    ]);
    const stepTypes = await agent.executedStepTypes(sessionId);
    assert.equal(stepTypes.filter((stepType) => stepType === "PollToolTask").length, 3);
    assert.equal(stepTypes.at(-1), "RunModel");
  });

  test("a task whose poll throws ToolExecutionError records the call failed", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("failed-task");
    await agent.sessions.startSession(sessionId, `${callReport({ topic: "q2", polls: 2, fail: true })} ;; pause held`);
    const record = await heldRecord(agent, sessionId);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(record.status, "failed");
    assert.equal(snapshot.messages.at(-1)?.text, "report: The tool call failed: the report on q2 failed");
  });

  test("cancel_turn cancels the source task, and the next turn still answers", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("cancelled-task");
    await agent.sessions.startSession(sessionId, callReport({ topic: "q3", hold: true }));
    const waiting = await waitForToolCallStatus(agent, sessionId, "waiting_for_task");
    const taskId = `task-${waiting.toolCallId}`;

    assert.equal(await agent.sessions.cancelTurn(sessionId), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);
    const note = snapshot.messages.at(-1)?.text ?? "";
    assert.ok(note.startsWith(turnCancelledNoteHeader), note);
    assert.ok(note.includes(`-> outcome_unknown: ${outcomeUnknownToolResult}`), note);
    // The source sees the abandoned record with its task ID and cancellation reason.
    assert.equal(
      agent.invocations.names().filter((name) => name === `cancel-task ${taskId} waiting_for_task turn_cancelled`).length,
      1,
    );
    const finished = (await agent.listEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { readonly status: string }).status);
    assert.deepEqual(finished, ["outcome_unknown"]);
    assert.ok((await agent.executedStepTypes(sessionId)).includes("CancelToolTask"));

    // The resumed Claude session has no tool_use without a tool_result.
    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say after"), true);
    const afterSnapshot = await agent.waitForTurns(sessionId, 2);
    assert.equal(afterSnapshot.messages.at(-1)?.text, "said: after");
  });

  test("a source call that asks a question waits for the answer, and the resumed session gets the result", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("question");
    await agent.sessions.startSession(sessionId, "call choose_account {} ;; pause held");
    const interrupt = await agent.waitForNewInterrupt(sessionId, "choose_account");
    assert.equal(interrupt.kind, "question");
    assert.equal(interrupt.question, "Which account?");

    assert.equal(await agent.sessions.answerQuestion(sessionId, interrupt.interruptId, "savings"), true);
    const record = await heldRecord(agent, sessionId);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(record.status, "succeeded");
    assert.equal(record.output, "account savings");
    assert.equal(snapshot.messages.at(-1)?.text, "choose_account: account savings");
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.deepEqual(
      agent.invocations.names().filter((name) => name.startsWith("choose-account ")),
      ["choose-account 0", "choose-account 1"],
    );
  });
});
