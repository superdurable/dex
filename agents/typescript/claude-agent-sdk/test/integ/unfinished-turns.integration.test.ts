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
  outcomeUnknownToolResult,
  toolCallCancelledBySteeringResult,
  type AgentMessage,
  type AgentToolCall,
  type AgentTurnContext,
} from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import {
  assertNoTurnNote,
  assertUniqueTranscriptEntries,
  deferralsOf,
  firstNewMessageOf,
  resultsOf,
  storedTranscriptEntries,
} from "../fixtures/transcript-assertions.js";

const lookupThenPublish = 'call lookup {"query":"a"} && call publish {"message":"b"}';
const turnOneUntilDeferral = [
  ["user", "turn-1", null],
  ["assistant", "turn-1", "lookup"],
  ["tool", "turn-1", "lookup"],
  ["assistant", "turn-1", "publish"],
];

describe("Claude agent turns store their transcript while they run and when they end early", () => {
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

  test("a deferred call stores the turn so far, and the completed turn stores no entry twice", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("incremental");
    await agent.sessions.startSession(sessionId, lookupThenPublish);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    const waiting = await agent.sessions.getAgentSnapshot(sessionId);
    assert.equal(waiting.state?.status, "running_tools");
    assert.deepEqual(rolesOf(waiting.messages), turnOneUntilDeferral);
    assert.equal(waiting.messages[2]?.toolOutput, "found a");
    assert.equal(waiting.messages[3]?.toolCallId, interrupt.toolCallId);
    assert.equal(waiting.messages[3]?.toolArguments, '{"message":"b"}');

    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.deepEqual(snapshot.messages.slice(0, waiting.messages.length), waiting.messages);
    assert.deepEqual(rolesOf(snapshot.messages), [
      ...turnOneUntilDeferral,
      ["tool", "turn-1", "publish"],
      ["assistant", "turn-1", null],
    ]);
    assert.equal(snapshot.messages[4]?.toolCallId, interrupt.toolCallId);
    assert.equal(snapshot.messages[4]?.toolOutput, "published b");
    assert.equal(snapshot.messages.at(-1)?.text, "lookup: found a; publish: published b");
    assertUniqueTranscriptEntries(snapshot.messages);
    assert.deepEqual(agent.invocations.names(), ["cli:new", "model_call", "lookup", "publish", "cli:resume", "model_call"]);
  });

  test("a turn steered at an approval stores the call's steering result instead of a note", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("steered");
    await agent.sessions.startSession(sessionId, lookupThenPublish);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");

    assert.equal(await agent.sessions.steerUserMessage(sessionId, "say after"), true);
    const snapshot = await agent.waitForTurns(sessionId, 2);

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.deepEqual(rolesOf(snapshot.messages), [
      ...turnOneUntilDeferral,
      ["tool", "turn-1", "publish"],
      ["user", "turn-2", null],
      ["assistant", "turn-2", null],
    ]);
    const steeredResult = snapshot.messages[4];
    assert.equal(steeredResult?.toolCallId, interrupt.toolCallId);
    assert.equal(steeredResult?.toolOutput, toolCallCancelledBySteeringResult);
    assert.equal(snapshot.messages.at(-1)?.text, "said: after");
    assertNoTurnNote(snapshot.messages);
    assertUniqueTranscriptEntries(snapshot.messages);
    // The next turn's CLI finds no pending deferral, so it runs only the new prompt.
    assert.deepEqual(agent.invocations.names(), ["cli:new", "model_call", "lookup", "cli:resume", "model_call"]);
    const steeredEvent = (await agent.listEvents(sessionId)).find((event) => event.type === "turn_steered");
    assert.deepEqual(JSON.parse(steeredEvent?.payload ?? "{}"), { message_count: 4, steered_message_count: 1 });
  });

  test("a turn cancelled while the resumed CLI runs stores the recorded write result", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("cancelled");
    await agent.sessions.startSession(sessionId, `${lookupThenPublish} ;; pause resumed`);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await agent.gate("resumed").waitUntilReached();

    assert.equal(await agent.sessions.cancelTurn(sessionId), true);
    const cancelled = await agent.waitForTurns(sessionId, 1);

    // The cancelled segment stored nothing; the call's result comes from its record.
    assert.deepEqual(rolesOf(cancelled.messages), [...turnOneUntilDeferral, ["tool", "turn-1", "publish"]]);
    assert.equal(cancelled.messages[4]?.toolCallId, interrupt.toolCallId);
    assert.equal(cancelled.messages[4]?.toolOutput, "published b");
    assertNoTurnNote(cancelled.messages);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say next"), true);
    const snapshot = await agent.waitForTurns(sessionId, 2);
    assert.equal(snapshot.messages.at(-1)?.text, "said: next");
    assertUniqueTranscriptEntries(snapshot.messages);
    assert.equal(agent.invocations.count("publish"), 1);
    assert.equal(agent.invocations.count("cli:resume"), 2);
  });

  test("a run state of an earlier adapter is stored before the abandoned call's result", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("legacy-run-state");
    await agent.sessions.startSession(sessionId, lookupThenPublish);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    const waiting = await agent.sessions.getAgentSnapshot(sessionId);
    const [record] = waiting.toolCalls;
    assert.ok(record !== undefined);
    const rejected: AgentToolCall = { ...record, status: "rejected", rejectionMessage: "not now" };
    const claudeSessionId = (JSON.parse(waiting.messages[1]?.frameworkItem ?? "{}") as { session_id: string }).session_id;
    const runner = agent.flow.runner;

    // This adapter stored the turn so far; an earlier one kept the same entries in the run state instead.
    const stored = runner.buildUnfinishedTurnMessages(
      endedTurnContext(waiting.messages, JSON.stringify({ version: 1, session_id: claudeSessionId, turn_entries: [] }), [rejected]),
    );
    const legacyRunState = JSON.stringify({
      version: 1,
      session_id: claudeSessionId,
      turn_entries: storedTranscriptEntries(waiting.messages),
    });
    const legacy = runner.buildUnfinishedTurnMessages(endedTurnContext(waiting.messages.slice(0, 1), legacyRunState, [rejected]));

    assert.deepEqual(
      stored.map((message) => [message.role, message.toolName, message.toolCallId, message.toolOutput]),
      [["tool", "publish", interrupt.toolCallId, "not now"]],
    );
    assert.deepEqual(
      legacy.map((message) => [message.role, message.toolName]),
      [...turnOneUntilDeferral.slice(1).map(([role, , toolName]) => [role, toolName]), ["tool", "publish"]],
    );
    assert.deepEqual(
      [legacy.at(-1)?.toolCallId, legacy.at(-1)?.toolOutput],
      [interrupt.toolCallId, "not now"],
    );
    // Message boundaries may differ, since trailing entries ride along with the next message; entries do not.
    assert.deepEqual(
      storedTranscriptEntries(legacy),
      storedTranscriptEntries([...waiting.messages.slice(1), ...stored]),
    );
    // The same committed state always gives the same messages.
    assert.deepEqual(
      runner.buildUnfinishedTurnMessages(endedTurnContext(waiting.messages.slice(0, 1), legacyRunState, [rejected])),
      legacy,
    );
  });

  test("the next turn answers a call that a Worker without buildUnfinishedTurnMessages left open", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("older-worker-end");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"draft"}');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    const ended = await agent.cancelTurnWithoutUnfinishedTurnMessages(sessionId, 1);
    // The core's note is all such a Worker stores; the stored tool use keeps no result.
    assert.deepEqual(rolesOf(ended.messages), [
      ["user", "turn-1", null],
      ["assistant", "turn-1", "publish"],
      ["user", "turn-1", null],
    ]);
    assert.equal(ended.messages[2]?.origin, "turn_cancelled_note");

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say next"), true);
    const snapshot = await agent.waitForTurns(sessionId, 2);

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.deepEqual(rolesOf(snapshot.messages), [
      ...rolesOf(ended.messages),
      ["user", "turn-2", null],
      ["tool", "turn-2", "publish"],
      ["assistant", "turn-2", null],
    ]);
    assert.equal(snapshot.messages[4]?.toolCallId, interrupt.toolCallId);
    assert.equal(snapshot.messages[4]?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, "said: next");
    assertUniqueTranscriptEntries(snapshot.messages);
    // The CLI found no pending deferral, so it ran only the new prompt.
    assert.deepEqual(agent.invocations.names(), ["cli:new", "model_call", "cli:resume", "model_call"]);

    // Later turns find every tool use answered and add nothing.
    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say again"), true);
    const later = await agent.waitForTurns(sessionId, 3);
    assert.deepEqual(rolesOf(later.messages.slice(snapshot.messages.length)), [
      ["user", "turn-3", null],
      ["assistant", "turn-3", null],
    ]);
    assertUniqueTranscriptEntries(later.messages);
  });

  test("a call left open behind a turn that an older Worker completed is answered right after its tool use", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("older-worker-turns");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"draft"}');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    await agent.cancelTurnWithoutUnfinishedTurnMessages(sessionId, 1);
    const afterOlderTurn = await agent.withoutEndedTurnAnswers(async () => {
      assert.equal(await agent.sessions.sendUserMessage(sessionId, "say second"), true);
      return agent.waitForTurns(sessionId, 2);
    });
    assert.equal(afterOlderTurn.messages.at(-1)?.text, "said: second");
    // The older Worker's CLI deferred the open call again and stored no result for it.
    assert.equal(deferralsOf(afterOlderTurn.messages, interrupt.toolCallId), 2);
    assert.equal(resultsOf(afterOlderTurn.messages, interrupt.toolCallId), 0);

    const { result: snapshot, outcomes } = await agent.withFailedFirstAttempt("turn-3", async () => {
      assert.equal(await agent.sessions.sendUserMessage(sessionId, "say third"), true);
      return agent.waitForTurns(sessionId, 3);
    });

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.deepEqual(rolesOf(snapshot.messages.slice(afterOlderTurn.messages.length)), [
      ["user", "turn-3", null],
      ["tool", "turn-3", "publish"],
      ["assistant", "turn-3", null],
    ]);
    const answer = snapshot.messages[afterOlderTurn.messages.length + 1];
    assert.equal(answer?.toolCallId, interrupt.toolCallId);
    assert.equal(answer?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, "said: third");
    // The retried attempt built the same answer, and the session stored it once.
    assert.equal(outcomes.length, 2);
    assert.deepEqual(firstNewMessageOf(outcomes[1]), firstNewMessageOf(outcomes[0]));
    assert.equal(firstNewMessageOf(outcomes[0])?.frameworkItem, answer?.frameworkItem);
    assert.equal(resultsOf(snapshot.messages, interrupt.toolCallId), 1);
    assert.equal(deferralsOf(snapshot.messages, interrupt.toolCallId), 2);
    assertUniqueTranscriptEntries(snapshot.messages);
    assert.deepEqual(agent.invocations.names().slice(-4), ["cli:resume", "model_call", "cli:resume", "model_call"]);
    assert.equal(agent.invocations.count("publish"), 0);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say fourth"), true);
    const later = await agent.waitForTurns(sessionId, 4);
    assert.deepEqual(rolesOf(later.messages.slice(snapshot.messages.length)), [
      ["user", "turn-4", null],
      ["assistant", "turn-4", null],
    ]);
    assert.equal(deferralsOf(later.messages, interrupt.toolCallId), 2);
    assertUniqueTranscriptEntries(later.messages);
  });

  test("an approved CLI tool whose resumed turn was cancelled is recorded with an unknown outcome", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("cancelled-bash");
    await agent.sessions.startSession(sessionId, 'call Bash {"command":"ls"} ;; pause resumed');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "Bash");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await agent.gate("resumed").waitUntilReached();

    assert.equal(await agent.sessions.cancelTurn(sessionId), true);
    const cancelled = await agent.waitForTurns(sessionId, 1);

    // The resumed CLI ran Bash before the cancellation dropped its segment.
    assert.equal(agent.invocations.count("Bash"), 1);
    assert.deepEqual(rolesOf(cancelled.messages), [
      ["user", "turn-1", null],
      ["assistant", "turn-1", "Bash"],
      ["tool", "turn-1", "Bash"],
    ]);
    assert.equal(cancelled.messages[2]?.toolCallId, interrupt.toolCallId);
    assert.equal(cancelled.messages[2]?.toolOutput, outcomeUnknownToolResult);
    assertNoTurnNote(cancelled.messages);
    assertUniqueTranscriptEntries(cancelled.messages);
  });
});

// Only the members buildUnfinishedTurnMessages reads.
function endedTurnContext(
  messages: readonly AgentMessage[],
  frameworkRunState: string,
  toolCalls: readonly AgentToolCall[],
): AgentTurnContext {
  return {
    turnId: "turn-1",
    messages,
    frameworkRunState,
    toolCalls,
    recordedToolCall: (toolCallId: string) => toolCalls.find((toolCall) => toolCall.toolCallId === toolCallId),
  } as unknown as AgentTurnContext;
}

function rolesOf(messages: readonly AgentMessage[]): unknown[][] {
  return messages.map((message) => [message.role, message.turnId, message.toolName]);
}
