// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { after, afterEach, before, describe, test } from "node:test";

import { tool } from "@anthropic-ai/claude-agent-sdk";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import {
  claudeTranscriptItemKind,
  createClaudeAgentSessionFlow,
  userPromptItemKind,
} from "../../src/index.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";

describe("Claude agent turns that complete inside RunModel", () => {
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

  test("a read tool runs inline and the history replays through a resumed CLI session", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("read-tool");
    await agent.sessions.startSession(sessionId, 'call lookup {"query":"docs"}');
    const first = await agent.waitForTurns(sessionId, 1);

    assert.deepEqual(
      first.messages.map((message) => [message.role, message.toolName]),
      [
        ["user", null],
        ["assistant", "lookup"],
        ["tool", "lookup"],
        ["assistant", null],
      ],
    );
    assert.equal(first.messages[1]?.toolArguments, '{"query":"docs"}');
    assert.equal(first.messages[2]?.toolOutput, "found docs");
    assert.equal(first.messages.at(-1)?.text, "lookup: found docs");
    assert.deepEqual(first.toolCalls, []);
    assert.deepEqual(first.pendingInterrupts, []);
    assert.equal(JSON.parse(first.messages[0]?.frameworkItem ?? "{}").kind, userPromptItemKind);
    const transcriptItems = first.messages
      .slice(1)
      .map((message) => JSON.parse(message.frameworkItem) as { kind: string; session_id: string });
    assert.deepEqual(new Set(transcriptItems.map((item) => item.kind)), new Set([claudeTranscriptItemKind]));
    assert.equal(new Set(transcriptItems.map((item) => item.session_id)).size, 1);
    const toolEvents = (await agent.listEvents(sessionId))
      .filter((event) => event.type === "tool_call_started" || event.type === "tool_call_finished")
      .map((event) => [event.type, (JSON.parse(event.payload) as { tool_name: string }).tool_name]);
    assert.deepEqual(toolEvents, [
      ["tool_call_started", "lookup"],
      ["tool_call_finished", "lookup"],
    ]);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "recall"), true);
    const second = await agent.waitForTurns(sessionId, 2);
    assert.equal(second.messages.at(-1)?.text, 'history: call lookup {"query":"docs"}');
    assert.deepEqual(
      second.messages.map((message) => message.turnId),
      ["turn-1", "turn-1", "turn-1", "turn-1", "turn-2", "turn-2"],
    );
    assert.deepEqual(agent.invocations.names(), [
      "cli:new",
      "model_call",
      "lookup",
      "model_call",
      "cli:resume",
      "model_call",
    ]);
  });

  test("get_agent_snapshot returns the conversation, usage, and streamed text", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("snapshot");
    await agent.sessions.startSession(sessionId, null);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, "   "), false);
    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say hi"), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.deepEqual(snapshot.state, {
      status: "waiting_for_user_message",
      turnCount: 1,
      activeTurnId: null,
      nextMessageSequence: 3,
      lastTurnError: null,
    });
    assert.deepEqual(
      snapshot.messages.map((message) => [message.role, message.text]),
      [
        ["user", "say hi"],
        ["assistant", "said: hi"],
      ],
    );
    assert.deepEqual(snapshot.usage, {
      modelCalls: 1,
      inputTokens: 13,
      outputTokens: 5,
      cachedInputTokens: 3,
      reasoningTokens: 0,
    });
    assert.equal(snapshot.queuedMessageCount, 0);
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.deepEqual(await agent.textByAttempt(sessionId), [[1, "said: hi"]]);
    const [frameworkRunState] = await agent.sessions.client.getAttributes(
      sessionId,
      agent.flow.frameworkRunState,
    );
    assert.equal(frameworkRunState, undefined);
    const page = await agent.sessions.listAgentMessages(sessionId, null, 1);
    assert.deepEqual(page.messages.map((message) => message.text), ["said: hi"]);
    assert.equal(page.nextBeforeSequence, 2);
  });

  test("a custom tool named like a Claude Code tool is rejected", () => {
    for (const toolName of ["Bash", "Write", "Task", "mcp__other__lookup"]) {
      const collidingTool = tool(toolName, "Collides", {}, async () => ({ content: [] }));
      assert.throws(
        () => createClaudeAgentSessionFlow({ options: {}, customTools: [collidingTool] }, "CollidingAgent"),
        { name: "TypeError", message: new RegExp(`custom tool "${toolName}" collides`) },
      );
    }
  });

  test("a CLI error result fails the turn after three attempts and the next turn runs", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("cli-error");
    await agent.sessions.startSession(sessionId, "fail");
    const failed = await agent.waitForTurns(sessionId, 1);

    assert.match(failed.state?.lastTurnError ?? "", /scripted failure/);
    assert.deepEqual(failed.messages.map((message) => message.text), ["fail"]);
    assert.equal(agent.invocations.count("cli:new"), 3);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say recovered"), true);
    const recovered = await agent.waitForTurns(sessionId, 2);
    assert.equal(recovered.state?.lastTurnError, null);
    assert.equal(recovered.messages.at(-1)?.text, "said: recovered");
  });
});
