// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { after, afterEach, before, describe, test } from "node:test";

import {
  outcomeUnknownToolResult,
  toolCallCancelledBySteeringResult,
  toolCallCancelledByTurnCancellationResult,
} from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { fakeClaudeFixedToolUseIdSuffixFileName } from "../fixtures/fake-claude-cli-environment.js";
import {
  assertUniqueTranscriptEntries,
  deferralsOf,
  resultsOf,
  storedTranscriptEntries,
} from "../fixtures/transcript-assertions.js";

const reusedToolUseId = "toolu_0_reusedacrossturns";

describe("a tool use ID that a later turn reuses names a new call", () => {
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
    writeFileSync(join(harness.stateDirectory, fakeClaudeFixedToolUseIdSuffixFileName), "reusedacrossturns");
    return harness;
  }

  test("an approved call reusing an earlier turn's ID runs and resumes", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("reused-approved");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"one"}');
    const first = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, first.interruptId, true), true);
    await agent.waitForTurns(sessionId, 1);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, 'call publish {"message":"two"}'), true);
    const second = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.deepEqual([first.toolCallId, second.toolCallId], [reusedToolUseId, reusedToolUseId]);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, second.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 2);

    // The resumed CLI gets the second call's own result, not a refusal for the first call's ID.
    assert.equal(snapshot.messages.at(-1)?.text, "publish: published two");
    assert.equal(agent.invocations.count("publish"), 2);
    assert.equal(resultsOf(snapshot.messages, reusedToolUseId), 2);
    assertUniqueTranscriptEntries(snapshot.messages);
  });

  test("two cancelled calls with one ID get results with distinct uuids", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("reused-cancelled");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"one"}');
    await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.steerUserMessage(sessionId, 'call publish {"message":"two"}'), true);
    // Interrupt IDs repeat with the tool use ID, so wait for the steered turn's own approval.
    await agent.sessions.waitForAgentState(
      sessionId,
      (state) => state.activeTurnId === "turn-2" && state.status === "running_tools",
      "the steered turn's approval",
    );
    await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.cancelTurn(sessionId), true);
    const cancelled = await agent.waitForTurns(sessionId, 2);

    const results = storedTranscriptEntries(cancelled.messages).filter((entry) =>
      contentOf(entry).some((block) => block["type"] === "tool_result"),
    );
    assert.deepEqual(
      results.map((entry) => contentOf(entry)[0]?.["content"]),
      [toolCallCancelledBySteeringResult, toolCallCancelledByTurnCancellationResult],
    );
    assert.equal(new Set(results.map((entry) => entry["uuid"])).size, 2);
    assert.equal(new Set(results.map((entry) => entry["sourceToolAssistantUUID"])).size, 2);
    assertUniqueTranscriptEntries(cancelled.messages);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say next"), true);
    const snapshot = await agent.waitForTurns(sessionId, 3);
    assert.equal(snapshot.messages.at(-1)?.text, "said: next");
    assertUniqueTranscriptEntries(snapshot.messages);
    assert.equal(agent.invocations.count("publish"), 0);
  });

  test("a later call reusing an answered ID is answered after an older Worker ends it", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("reused-older-worker");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"one"}');
    const first = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, first.interruptId, true), true);
    await agent.waitForTurns(sessionId, 1);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, 'call publish {"message":"two"}'), true);
    await agent.waitForNewInterrupt(sessionId, "publish");
    const ended = await agent.cancelTurnWithoutUnfinishedTurnMessages(sessionId, 2);
    assert.equal(ended.messages.at(-1)?.origin, "turn_cancelled_note");
    assert.equal(resultsOf(ended.messages, reusedToolUseId), 1);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say next"), true);
    const snapshot = await agent.waitForTurns(sessionId, 3);

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.deepEqual(snapshot.pendingInterrupts, []);
    const answer = snapshot.messages[ended.messages.length + 1];
    assert.deepEqual(
      [answer?.turnId, answer?.toolCallId, answer?.toolOutput],
      ["turn-3", reusedToolUseId, outcomeUnknownToolResult],
    );
    assert.equal(snapshot.messages.at(-1)?.text, "said: next");
    assert.equal(resultsOf(snapshot.messages, reusedToolUseId), 2);
    assertUniqueTranscriptEntries(snapshot.messages);
    assert.equal(agent.invocations.count("publish"), 1);
  });

  test("a live deferral reusing a healed call's ID still resumes", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("reused-live-deferral");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"one"}');
    await agent.waitForNewInterrupt(sessionId, "publish");
    const ended = await agent.cancelTurnWithoutUnfinishedTurnMessages(sessionId, 1);

    // The next turn answers the first call, then defers a new call under the same ID.
    assert.equal(await agent.sessions.sendUserMessage(sessionId, 'call publish {"message":"two"}'), true);
    const second = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(second.toolCallId, reusedToolUseId);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, second.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 2);

    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages[ended.messages.length + 1]?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, "publish: published two");
    assert.equal(agent.invocations.count("publish"), 1);
    assert.equal(deferralsOf(snapshot.messages, reusedToolUseId), 2);
    assert.equal(resultsOf(snapshot.messages, reusedToolUseId), 2);
    assertUniqueTranscriptEntries(snapshot.messages);
  });
});

function contentOf(entry: Record<string, unknown>): Record<string, unknown>[] {
  const content = (entry["message"] as { readonly content?: unknown } | undefined)?.content;
  return Array.isArray(content) ? (content as Record<string, unknown>[]) : [];
}
