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
import { after, before, describe, test } from "node:test";

import type { AgentMessage } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  PrivateDexEngine,
  readMarkerLines,
} from "../../src/testing/index.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

describe("agent session turns with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let deadlineSessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedAgentFlow(markerDirectory);
    const deadlineFlow = createScriptedAgentFlow(markerDirectory, {
      flowType: "ScriptedAgentWithApprovalDeadline",
      interruptDeadlineMs: 1_000,
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, deadlineFlow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    deadlineSessions = new AgentSessionTestClient(environment.client, deadlineFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a turn with a read tool completes and its history replays on the next turn", async () => {
    const sessionId = newSessionId("read-tool");
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const firstSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(firstSnapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather",
      "2 turn-1 assistant tool_call get_weather",
      "3 turn-1 tool tool_result get_weather sunny in Paris",
      "4 turn-1 assistant get_weather -> sunny in Paris",
    ]);
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} turn-1-get_weather inline`]);
    assert.deepEqual(firstSnapshot.toolCalls, []);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(state.lastTurnError, null);
    assert.equal(state.nextMessageSequence, 7);

    const secondSnapshot = await sessions.getAgentSnapshot(sessionId);
    const answer = secondSnapshot.messages.at(-1);
    assert.equal(answer?.turnId, "turn-2");
    assert.equal(
      answer?.text,
      "history | user: call get_weather | assistant: get_weather -> sunny in Paris",
    );
    assert.deepEqual(secondSnapshot.usage, {
      modelCalls: 3,
      inputTokens: 30,
      outputTokens: 15,
      cachedInputTokens: 0,
      reasoningTokens: 0,
    });

    const eventTypes = (await sessions.listAgentEvents(sessionId)).map((event) => event.type);
    for (const expectedType of [
      "turn_started",
      "tool_call_started",
      "tool_call_finished",
      "text_delta",
      "usage_recorded",
      "turn_completed",
    ] as const) {
      assert.ok(eventTypes.includes(expectedType), `missing ${expectedType} in ${eventTypes.join(",")}`);
    }
  });

  test("get_agent_snapshot and list_agent_messages return the conversation after a turn", async () => {
    const sessionId = newSessionId("snapshot");
    await sessions.startSession(sessionId, null);
    assert.equal(await sessions.sendUserMessage(sessionId, "   "), false);
    assert.equal(await sessions.sendUserMessage(sessionId, "hello"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.state, {
      status: "waiting_for_user_message",
      turnCount: 1,
      activeTurnId: null,
      nextMessageSequence: 3,
      executingToolCallId: null,
      lastTurnError: null,
    });
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user hello",
      "2 turn-1 assistant echo: hello",
    ]);
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.equal(snapshot.queuedMessageCount, 0);
    assert.equal(snapshot.usage.modelCalls, 1);

    const newestPage = await sessions.listAgentMessages(sessionId, null, 1);
    assert.deepEqual(newestPage.messages.map(describeMessage), ["2 turn-1 assistant echo: hello"]);
    assert.equal(newestPage.nextBeforeSequence, 2);
    const olderPage = await sessions.listAgentMessages(sessionId, 2, 0);
    assert.deepEqual(olderPage.messages.map(describeMessage), ["1 turn-1 user hello"]);
    assert.equal(olderPage.nextBeforeSequence, null);
  });

  test("a rejected write never runs and the model sees the rejection message", async () => {
    const sessionId = newSessionId("rejected");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(interrupt.interruptId, "turn-1-send_email");
    assert.equal(interrupt.kind, "tool_approval");
    assert.equal(interrupt.deadline, null);
    const waitingState = await sessions.readAgentState(sessionId);
    assert.equal(waitingState?.status, "waiting_for_interrupt");
    const waitingSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waitingSnapshot.toolCalls.map((toolCall) => [toolCall.toolCallId, toolCall.effect, toolCall.status]),
      [["turn-1-send_email", "unclassified", "awaiting_approval"]],
    );

    assert.equal(
      await sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not today"),
      true,
    );
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "send_email -> The user rejected the tool call: not today",
    );
    assert.deepEqual(toolLog("send_email", sessionId), []);
    const eventTypes = (await sessions.listAgentEvents(sessionId)).map((event) => event.type);
    assert.ok(eventTypes.includes("tool_call_deferred"));
    assert.ok(eventTypes.includes("interrupt_raised"));
    assert.ok(eventTypes.includes("interrupt_resolved"));
  });

  test("resolve_interrupt called twice returns false the second time and the write runs once", async () => {
    const sessionId = newSessionId("resolve-twice");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");

    assert.equal(await sessions.resolveInterrupt(sessionId, "no-such-interrupt", true), false);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1-send_email`,
    );
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} turn-1-send_email attempt 1`]);
  });

  test("an idempotent write runs without approval and a tool failure reaches the model", async () => {
    const sessionId = newSessionId("idempotent");
    await sessions.startSession(sessionId, "call refund_order");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "refund_order -> refund issued",
    );
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} turn-1-refund_order attempt 1`]);

    assert.equal(await sessions.sendUserMessage(sessionId, "call delete_account"), true);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "delete_account");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "delete_account -> The tool call failed: account deletion is disabled",
    );
  });

  test("a second agent Flow type rejects an approval whose deadline passed", async () => {
    const sessionId = newSessionId("deadline");
    await deadlineSessions.startSession(sessionId, "call send_email");
    const interrupt = await deadlineSessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.ok(interrupt.deadline !== null);
    assert.equal(Date.parse(interrupt.deadline) - Date.parse(interrupt.createdAt), 1_000);

    await deadlineSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await deadlineSessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    const snapshot = await deadlineSessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "send_email -> The user rejected the tool call: approval deadline passed",
    );
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.deepEqual(toolLog("send_email", sessionId), []);
    const resolvedEvent = (await deadlineSessions.listAgentEvents(sessionId)).find(
      (event) => event.type === "interrupt_resolved",
    );
    assert.deepEqual(JSON.parse(resolvedEvent?.payload ?? "null"), {
      interrupt_id: "turn-1-send_email",
      approved: false,
      rejection_message: "approval deadline passed",
    });
  });

  function toolLog(toolName: string, sessionId: string): string[] {
    return readMarkerLines(join(markerDirectory, `${toolName}.log`)).filter((line) =>
      line.startsWith(`${sessionId} `),
    );
  }
});

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function describeMessage(message: AgentMessage): string {
  const item = JSON.parse(message.frameworkItem) as { kind: string };
  const detail =
    message.text ??
    [item.kind, message.toolName, message.toolOutput].filter((part) => part !== null).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}
