// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import {
  ToolOutcomeUnknownError,
  outcomeUnknownToolResult,
  type AgentEventWriter,
  type AgentToolCall,
  type DeferredToolExecution,
} from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentRunner, approvedCliToolOutput } from "../../src/index.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { renamedToolsVariable, slowWriteGateName } from "../fixtures/claude-test-agent.js";
import { assertUniqueTranscriptEntries } from "../fixtures/transcript-assertions.js";

describe("A Worker restarts with a deploy that renamed a tool the open turn already called", () => {
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
    harness = await ClaudeAgentTestHarness.start(engine, false);
    return harness;
  }

  test("a renamed completed write replays its recorded result", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-completed");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":5} ;; pause model-gate');
    const modelGate = agent.gate("model-gate");
    await modelGate.waitUntilReached();

    await firstWorker.kill();
    modelGate.release();
    await agent.startWorker({ [renamedToolsVariable]: "charge=bill" });

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    const toolResults = snapshot.messages.filter((message) => message.role === "tool");
    assert.equal(toolResults.length, 1);
    assert.equal(toolResults[0]?.toolName, "charge");
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `charge: charged 5 with key ${sessionId}/turn-1/${toolResults[0]?.toolCallId}`,
    );
    assert.equal(agent.invocations.count("charge"), 1);
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("a renamed in-flight single-attempt write records outcome_unknown", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-in-flight");
    await agent.sessions.startSession(sessionId, 'call slow_write {"message":"ledger"}');
    const writeGate = agent.gate(slowWriteGateName);
    await writeGate.waitUntilReached();

    await firstWorker.kill();
    await agent.startWorker({ [renamedToolsVariable]: "slow_write=save" });

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, `slow_write: ${outcomeUnknownToolResult}`);
    const toolResults = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolResults.map((message) => [message.toolName, message.toolOutput]),
      [["slow_write", outcomeUnknownToolResult]],
    );
    assert.equal(agent.invocations.count("slow_write"), 1);
    assert.equal(writeGate.reachedCount(), 1);
    assert.ok((await agent.executedStepTypes(sessionId)).includes("RecordToolOutcomeUnknown"));
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("a renamed idempotent write retried after a kill records outcome_unknown", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-retry");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":5,"gate":"charge-gate"} ;; pause model-gate');
    const chargeGate = agent.gate("charge-gate");
    await chargeGate.waitUntilReached();

    await firstWorker.kill();
    await agent.startWorker({ [renamedToolsVariable]: "charge=bill" });
    const modelGate = agent.gate("model-gate");
    await modelGate.waitUntilReached();

    // The turn's records leave the snapshot when it ends, so read the record while the model call waits.
    const { toolCalls } = await agent.sessions.getAgentSnapshot(sessionId);
    assert.equal(toolCalls.length, 1);
    const [record] = toolCalls;
    assert.equal(record?.toolName, "charge");
    assert.equal(record?.status, "outcome_unknown");
    assert.equal(record?.output, null);
    assert.equal(record?.error, removedCustomToolRetryError("charge"));
    modelGate.release();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, `charge: ${outcomeUnknownToolResult}`);
    const toolResults = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolResults.map((message) => [message.toolName, message.toolOutput]),
      [["charge", outcomeUnknownToolResult]],
    );
    assert.equal(agent.invocations.count("charge"), 1);
    assert.equal(chargeGate.reachedCount(), 1);
    const finishedStatuses = (await agent.listEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
    assert.deepEqual(finishedStatuses, ["outcome_unknown"]);
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("a renamed write approved on the new Worker keeps its placeholder record and gives the model outcome_unknown", async () => {
    const agent = await startHarness();
    const firstWorker = await agent.startWorker();
    const sessionId = agent.newSessionId("renamed-approved");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"hello"} ;; pause model-gate');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");

    await firstWorker.kill();
    await agent.startWorker({ [renamedToolsVariable]: "publish=post" });
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const modelGate = agent.gate("model-gate");
    await modelGate.waitUntilReached();

    // A call that needed approval looks like a Claude Code tool to its tool Step.
    const { toolCalls } = await agent.sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      toolCalls.map((record) => [record.toolName, record.status, record.output]),
      [["publish", "succeeded", approvedCliToolOutput]],
    );
    modelGate.release();

    const snapshot = await agent.waitForTurns(sessionId, 1);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, `publish: ${outcomeUnknownToolResult}`);
    const toolResults = snapshot.messages.filter((message) => message.role === "tool");
    assert.deepEqual(
      toolResults.map((message) => [message.toolName, message.toolOutput]),
      [["publish", outcomeUnknownToolResult]],
    );
    assert.equal(agent.invocations.count("publish"), 0);
    const finishedStatuses = (await agent.listEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
    assert.deepEqual(finishedStatuses, ["outcome_unknown"]);
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("only a retry of a call without approval treats a missing tool as a removed custom tool", async () => {
    const runner = new ClaudeAgentRunner({});
    const removedCustomToolCall = toolCallRecord("charge", false);

    await assert.rejects(runner.executeDeferredToolCall(deferredToolExecution(removedCustomToolCall, 2)), (error) => {
      assert.ok(error instanceof ToolOutcomeUnknownError);
      assert.equal(error.message, removedCustomToolRetryError("charge"));
      return true;
    });
    // No earlier attempt ran the call.
    assert.deepEqual(await runner.executeDeferredToolCall(deferredToolExecution(removedCustomToolCall, 1)), {
      output: approvedCliToolOutput,
      isFinishedByResumedTurn: true,
    });
    // A Claude Code tool defers only for approval, so a call with approval may be one.
    for (const toolName of ["Bash", "charge"]) {
      assert.deepEqual(await runner.executeDeferredToolCall(deferredToolExecution(toolCallRecord(toolName, true), 2)), {
        output: approvedCliToolOutput,
        isFinishedByResumedTurn: true,
      });
    }
  });
});

function removedCustomToolRetryError(toolName: string): string {
  return `${toolName} did not run on this attempt: this Worker no longer offers it, and an earlier attempt may have run it.`;
}

function toolCallRecord(toolName: string, requiresApproval: boolean): AgentToolCall {
  return {
    toolCallId: `toolu_${toolName}`,
    turnId: "turn-1",
    toolName,
    arguments: "{}",
    effect: "idempotent_write",
    requiresApproval,
    status: "approved",
    output: null,
    error: null,
    rejectionMessage: null,
    deferralOrder: 0,
  };
}

function deferredToolExecution(toolCall: AgentToolCall, attempt: number): DeferredToolExecution {
  return {
    flowId: "renamed-tools",
    toolCall,
    idempotencyKey: `renamed-tools/${toolCall.turnId}/${toolCall.toolCallId}`,
    attempt,
    events: silentEvents,
    cancellationSignal: new AbortController().signal,
    deadline: undefined,
    heartbeat: async () => undefined,
  };
}

const silentEvents: AgentEventWriter = {
  turnId: "turn-1",
  attempt: 1,
  writeEvent: () => undefined,
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
  writeToolCallStarted: () => undefined,
  writeToolCallFinished: () => undefined,
};
