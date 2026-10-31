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
  turnFailureNoteFooter,
  turnFailureNoteHeader,
} from "@superdurable/dex-agents";
import { PrivateDexEngine } from "@superdurable/dex-agents/testing";

import { claudeTranscriptItemKind } from "../../src/index.js";
import { ClaudeAgentTestHarness } from "../fixtures/claude-agent-test-harness.js";
import { fakeClaudeResumeWithoutHooksVariable } from "../fixtures/fake-claude-cli-environment.js";

const callAloneResult = "did not run. It must run outside this turn";

describe("Claude agent tool calls deferred to their own Steps", () => {
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

  test("a rejected write never runs and the model sees the rejection message", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("rejected");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"hello"}');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    const waiting = await agent.sessions.getAgentSnapshot(sessionId);
    assert.equal(waiting.state?.status, "running_tools");
    assert.deepEqual(
      waiting.toolCalls.map((toolCall) => [toolCall.toolCallId, toolCall.effect, toolCall.status, toolCall.arguments]),
      [[interrupt.toolCallId, "non_idempotent_write", "awaiting_approval", '{"message":"hello"}']],
    );

    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not today"), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "publish: not today");
    assert.equal(snapshot.messages.find((message) => message.role === "tool")?.toolOutput, "not today");
    assert.equal(agent.invocations.count("publish"), 0);
    const eventTypes = (await agent.listEvents(sessionId)).map((event) => event.type);
    for (const expectedType of ["tool_call_deferred", "interrupt_raised", "interrupt_resolved"]) {
      assert.ok(eventTypes.includes(expectedType), `missing ${expectedType} in ${eventTypes.join(",")}`);
    }
  });

  test("an approved write runs once and resolve_interrupt called twice returns false the second time", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("approved");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"hello"}');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");

    assert.equal(await agent.sessions.resolveInterrupt(sessionId, "no-such-interrupt", true), false);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.deepEqual(
      snapshot.messages.map((message) => [message.role, message.toolName, message.text ?? message.toolOutput]),
      [
        ["user", null, 'call publish {"message":"hello"}'],
        ["assistant", "publish", null],
        ["tool", "publish", "published hello"],
        ["assistant", null, "publish: published hello"],
      ],
    );
    assert.equal(snapshot.messages[1]?.toolCallId, interrupt.toolCallId);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.deepEqual(agent.invocations.names(), [
      "cli:new",
      "model_call",
      "publish",
      "cli:resume",
      "model_call",
    ]);
    assert.equal(snapshot.usage.modelCalls, 2);
  });

  test("a write rewritten by an application hook runs once with the rewritten input", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("rewritten");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"rewrite:hello"}');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(interrupt.arguments, '{"message":"reviewed hello"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "publish: published reviewed hello");
    assert.equal(agent.invocations.count("publish"), 1);
  });

  test("a deferred call whose rewritten input fails the tool's schema is recorded failed and never runs", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("invalid-input");
    await agent.sessions.startSession(sessionId, 'call publish {"message":"rewrite-invalid"} ;; pause invalid');
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(interrupt.arguments, '{"message":5}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const invalidGate = agent.gate("invalid");
    await invalidGate.waitUntilReached();

    const [record] = (await agent.sessions.getAgentSnapshot(sessionId)).toolCalls;
    assert.equal(record?.status, "failed");
    assert.match(record?.error ?? "", /^invalid arguments for publish: /);
    invalidGate.release();
    await agent.waitForTurns(sessionId, 1);
    assert.equal(agent.invocations.count("publish"), 0);
  });

  test("a resumed call that skips the hooks is answered from its record through canUseTool", async () => {
    harness = await ClaudeAgentTestHarness.start(engine, true, () => ({
      flowType: "ScriptedClaudeAgentResumingWithoutHooks",
      environment: { [fakeClaudeResumeWithoutHooksVariable]: "1" },
    }));
    const agent = harness;
    const approvedSessionId = agent.newSessionId("without-hooks-approved");
    await agent.sessions.startSession(approvedSessionId, 'call publish {"message":"hello"}');
    const approval = await agent.waitForNewInterrupt(approvedSessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(approvedSessionId, approval.interruptId, true), true);
    const approved = await agent.waitForTurns(approvedSessionId, 1);
    assert.equal(approved.messages.at(-1)?.text, "publish: published hello");
    assert.equal(agent.invocations.count("publish"), 1);
    assert.equal(agent.invocations.count("can_use_tool mcp__tools__publish"), 1);

    const rejectedSessionId = agent.newSessionId("without-hooks-rejected");
    await agent.sessions.startSession(rejectedSessionId, 'call publish {"message":"draft"}');
    const rejection = await agent.waitForNewInterrupt(rejectedSessionId, "publish");
    assert.equal(
      await agent.sessions.resolveInterrupt(rejectedSessionId, rejection.interruptId, false, "not today"),
      true,
    );
    const rejected = await agent.waitForTurns(rejectedSessionId, 1);
    assert.equal(rejected.messages.at(-1)?.text, "publish: not today");
    assert.equal(agent.invocations.count("publish"), 1);
    assert.equal(agent.invocations.count("can_use_tool mcp__tools__publish"), 2);
  });

  test("an idempotent write runs in its own Step with a stable idempotency key across retries", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("idempotent");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":5,"fail_attempts":1}');
    const snapshot = await agent.waitForTurns(sessionId, 1);

    const toolCallId = snapshot.messages[1]?.toolCallId;
    assert.equal(snapshot.messages.at(-1)?.text, `charge: charged 5 with key ${sessionId}/turn-1/${toolCallId}`);
    assert.equal(agent.invocations.count("charge"), 2);
    assert.deepEqual(snapshot.pendingInterrupts, []);
  });

  test("an idempotent write whose attempts run out records outcome_unknown", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("idempotent-exhausted");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":5,"fail_attempts":3}');
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, `charge: ${outcomeUnknownToolResult}`);
    assert.equal(agent.invocations.count("charge"), 3);
  });

  test("a custom tool error result is recorded as failed and shown to the model", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("tool-error");
    await agent.sessions.startSession(sessionId, 'call charge {"amount":0}');
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(snapshot.messages.at(-1)?.text, "charge: The tool call failed: amount must be positive");
    assert.equal(agent.invocations.count("charge"), 1);
  });

  test("only the first write of a reply is deferred; a later one is refused and called again alone", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("parallel");
    await agent.sessions.startSession(
      sessionId,
      'call lookup {"query":"a"} && call publish {"message":"b"} && call publish {"message":"c"}',
    );
    const firstInterrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(firstInterrupt.arguments, '{"message":"b"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, firstInterrupt.interruptId, true), true);
    const secondInterrupt = await agent.waitForNewInterrupt(sessionId, "publish", [firstInterrupt.interruptId]);
    assert.equal(secondInterrupt.arguments, '{"message":"c"}');
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, secondInterrupt.interruptId, true), true);
    const snapshot = await agent.waitForTurns(sessionId, 1);

    assert.equal(
      snapshot.messages.at(-1)?.text,
      "lookup: found a; publish: published b; publish: published c",
    );
    const refusedResults = snapshot.messages.filter(
      (message) => message.role === "tool" && message.toolOutput?.includes(callAloneResult) === true,
    );
    assert.equal(refusedResults.length, 1);
    assert.equal(refusedResults[0]?.toolCallId?.startsWith("toolu_2_"), true);
    assert.equal(agent.invocations.count("lookup"), 1);
    assert.equal(agent.invocations.count("publish"), 2);
  });

  test("a built-in CLI tool waits for approval, then runs inside the resumed turn", async () => {
    const agent = await startHarness();
    const approvedSessionId = agent.newSessionId("bash-approved");
    await agent.sessions.startSession(approvedSessionId, 'call Bash {"command":"ls"}');
    const interrupt = await agent.waitForNewInterrupt(approvedSessionId, "Bash");
    const [record] = (await agent.sessions.getAgentSnapshot(approvedSessionId)).toolCalls;
    assert.equal(record?.effect, "unclassified");
    assert.equal(agent.invocations.count("Bash"), 0);
    assert.equal(await agent.sessions.resolveInterrupt(approvedSessionId, interrupt.interruptId, true), true);
    const approved = await agent.waitForTurns(approvedSessionId, 1);
    assert.equal(approved.messages.at(-1)?.text, "Bash: ran ls");
    assert.equal(agent.invocations.count("Bash"), 1);

    const rejectedSessionId = agent.newSessionId("bash-rejected");
    await agent.sessions.startSession(rejectedSessionId, 'call Bash {"command":"rm -rf build"}');
    const rejection = await agent.waitForNewInterrupt(rejectedSessionId, "Bash");
    assert.equal(
      await agent.sessions.resolveInterrupt(rejectedSessionId, rejection.interruptId, false, "not on this machine"),
      true,
    );
    const rejected = await agent.waitForTurns(rejectedSessionId, 1);
    // Like the real CLI, a PreToolUse denial reaches the model with a hook prefix.
    assert.equal(rejected.messages.at(-1)?.text, "Bash: PreToolUse:Bash hook error: not on this machine");
    assert.equal(agent.invocations.count("Bash"), 1);
  });

  test("a resumed segment that fails after a write leaves a note that the next turn sends", async () => {
    const agent = await startHarness();
    const sessionId = agent.newSessionId("failed-after-write");
    const firstPrompt = 'call publish {"message":"draft"} ;; fail';
    await agent.sessions.startSession(sessionId, firstPrompt);
    const interrupt = await agent.waitForNewInterrupt(sessionId, "publish");
    assert.equal(await agent.sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const failed = await agent.waitForTurns(sessionId, 1);

    const note = [
      turnFailureNoteHeader,
      '- publish {"message":"draft"} -> succeeded: published draft',
      turnFailureNoteFooter,
    ].join("\n");
    assert.match(failed.state?.lastTurnError ?? "", /scripted failure after tool calls/);
    assert.deepEqual(
      failed.messages.map((message) => [message.role, message.turnId, message.text]),
      [
        ["user", "turn-1", firstPrompt],
        ["user", "turn-1", note],
      ],
    );
    assert.equal(agent.invocations.count("cli:resume"), 3);
    assert.equal(agent.invocations.count("publish"), 1);

    assert.equal(await agent.sessions.sendUserMessage(sessionId, "say next"), true);
    const snapshot = await agent.waitForTurns(sessionId, 2);
    assert.equal(snapshot.messages.at(-1)?.text, "said: next");
    const cliPrompts = snapshot.messages
      .filter((message) => message.turnId === "turn-2")
      .map((message) => JSON.parse(message.frameworkItem) as { kind: string; entries?: Record<string, unknown>[] })
      .filter((item) => item.kind === claudeTranscriptItemKind)
      .flatMap((item) => item.entries ?? [])
      .filter((entry) => entry["type"] === "user")
      .map((entry) => (entry["message"] as { content?: unknown } | undefined)?.content)
      .filter((content): content is { text: string }[] => isTextBlockList(content))
      .map((content) => content.map((block) => block.text));
    assert.deepEqual(cliPrompts, [[firstPrompt, note, "say next"]]);
    assert.equal(agent.invocations.count("publish"), 1);
  });
});

function isTextBlockList(content: unknown): content is { text: string }[] {
  return Array.isArray(content) && content.length > 0 && content.every((block) => block?.type === "text");
}
