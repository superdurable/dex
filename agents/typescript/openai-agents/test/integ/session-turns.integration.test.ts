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

import {
  messageSequenceKey,
  outcomeUnknownToolResult,
  type AgentEvent,
  type AgentMessage,
} from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "@superdurable/dex-agents/testing";

import { createScriptedOpenAIAgentFlow } from "../fixtures/scripted-openai-agent.js";

describe("OpenAI Agents SDK sessions in Step mode with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedOpenAIAgentFlow(markerDirectory);
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a read tool runs as RunModel, CallTool, ResumeTurn, RunModel, and the history replays", async () => {
    const sessionId = newSessionId("read-tool");
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const firstSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(firstSnapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather",
      "2 turn-1 assistant function_call get_weather",
      "3 turn-1 tool function_call_result get_weather sunny in Paris",
      "4 turn-1 assistant get_weather -> sunny in Paris",
    ]);
    assert.equal(firstSnapshot.messages[1]?.toolCallId, "call_1_get_weather");
    assert.equal(firstSnapshot.messages[1]?.toolArguments, '{"city":"Paris"}');
    // The tool ran once, in its own Step, with the context createContext built.
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} call_1_get_weather attempt 1`]);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(state.lastTurnError, null);
    const secondSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      secondSnapshot.messages.at(-1)?.text,
      "history | user: call get_weather | assistant: get_weather -> sunny in Paris",
    );
    assert.deepEqual(secondSnapshot.usage, {
      modelCalls: 3,
      inputTokens: 30,
      outputTokens: 15,
      cachedInputTokens: 6,
      reasoningTokens: 3,
    });
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "AwaitUserMessage",
      "RunModel",
    ]);

    const events = await sessions.listAgentEvents(sessionId);
    const toolEvents = events
      .filter((event) => event.turnId === "turn-1" && event.type.startsWith("tool_call_"))
      .map((event) => [event.type, JSON.parse(event.payload)]);
    assert.deepEqual(toolEvents, [
      [
        "tool_call_deferred",
        { tool_call_id: "call_1_get_weather", tool_name: "get_weather", effect: "read", requires_approval: false },
      ],
      ["tool_call_started", { tool_call_id: "call_1_get_weather", tool_name: "get_weather" }],
      ["tool_call_finished", { tool_call_id: "call_1_get_weather", tool_name: "get_weather", status: "succeeded" }],
    ]);
    assert.deepEqual(readTexts(events, "text_delta", "turn-1"), ["get_weather -> sunny in Paris"]);
  });

  test("get_agent_snapshot and list_agent_messages return the conversation after a turn", async () => {
    const sessionId = newSessionId("snapshot");
    await sessions.startSession(sessionId, null);
    assert.equal(await sessions.sendUserMessage(sessionId, "   "), false);
    assert.equal(await sessions.sendUserMessage(sessionId, "hello"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), ["1 turn-1 user hello", "2 turn-1 assistant echo: hello"]);
    assert.deepEqual(JSON.parse(snapshot.messages[0]?.frameworkItem ?? "null"), {
      type: "message",
      role: "user",
      content: "hello",
    });
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.equal(snapshot.usage.modelCalls, 1);
    const [storedAnswer] = await environment.client.getAttributes(
      sessionId,
      sessions.flow.agentMessages.instance(messageSequenceKey(2)),
    );
    assert.equal(storedAnswer?.text, "echo: hello");
    const newestPage = await sessions.listAgentMessages(sessionId, null, 1);
    assert.deepEqual(newestPage.messages.map(describeMessage), ["2 turn-1 assistant echo: hello"]);
    assert.equal(newestPage.nextBeforeSequence, 2);
    const olderPage = await sessions.listAgentMessages(sessionId, 2, 50);
    assert.deepEqual(olderPage.messages.map(describeMessage), ["1 turn-1 user hello"]);
    assert.equal(olderPage.nextBeforeSequence, null);
  });

  test("a rejected write never runs and the model sees the rejection message", async () => {
    const sessionId = newSessionId("rejected");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(interrupt.interruptId, "call_1_send_email");
    assert.equal(interrupt.arguments, '{"to":"ana@example.com"}');
    const [frameworkRunState] = await environment.client.getAttributes(sessionId, sessions.flow.frameworkRunState);
    assert.equal(typeof (JSON.parse(frameworkRunState ?? "null") as { $schemaVersion?: unknown }).$schemaVersion, "string");

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not today"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call send_email",
      "2 turn-1 assistant function_call send_email",
      "3 turn-1 tool function_call_result send_email not today",
      "4 turn-1 assistant send_email -> not today",
    ]);
    assert.deepEqual(toolLog("send_email", sessionId), []);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "ResumeTurn",
      "RunModel",
    ]);
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
    assert.equal(snapshot.messages.at(-1)?.text, `send_email -> email sent for ${sessionId}/turn-1/call_1_send_email`);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call_1_send_email attempt 1`]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("an idempotent write runs without approval and a ToolExecutionError reaches the model", async () => {
    const sessionId = newSessionId("idempotent");
    await sessions.startSession(sessionId, "call refund_order");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "refund_order -> refund issued");
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} call_1_refund_order attempt 1`]);

    assert.equal(await sessions.sendUserMessage(sessionId, "call delete_account"), true);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "delete_account");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "delete_account -> The tool call failed: account deletion is disabled",
    );
    assert.deepEqual(toolLog("delete_account", sessionId), [`${sessionId} call_2_delete_account attempt 1`]);
  });

  test("tools that keep the default errorFunction never record its text as a success", async () => {
    const writeSessionId = newSessionId("default-error-write");
    await sessions.startSession(writeSessionId, "call post_update");
    await sessions.waitForCompletedTurns(writeSessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(writeSessionId)).messages.at(-1)?.text,
      `post_update -> ${outcomeUnknownToolResult}`,
    );
    assert.deepEqual(toolLog("post_update", writeSessionId), [`${writeSessionId} call_1_post_update attempt 1`]);

    const readSessionId = newSessionId("default-error-read");
    await sessions.startSession(readSessionId, "call read_inventory");
    await sessions.waitForCompletedTurns(readSessionId, 1, { timeoutMs: 120_000 });
    const answer = (await sessions.getAgentSnapshot(readSessionId)).messages.at(-1)?.text ?? "";
    assert.match(answer, /^read_inventory -> The tool call failed: .*SDK default text/);
    assert.equal(toolLog("read_inventory", readSessionId).length, 3);
  });

  test("a read tool with its own needsApproval waits for approval, then runs in CallTool", async () => {
    const sessionId = newSessionId("native-approval");
    await sessions.startSession(sessionId, "call lookup_invoice");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "lookup_invoice");
    const [record] = (await sessions.getAgentSnapshot(sessionId)).toolCalls;
    assert.deepEqual([record?.effect, record?.requiresApproval, record?.status], ["read", true, "awaiting_approval"]);
    assert.deepEqual(toolLog("lookup_invoice", sessionId), []);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "lookup_invoice -> invoice 7 is paid");
    assert.deepEqual(toolLog("lookup_invoice", sessionId), [`${sessionId} call_1_lookup_invoice attempt 1`]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("tool calls of one model response run in parallel Steps", async () => {
    const sessionId = newSessionId("parallel");
    await sessions.startSession(sessionId, "call lookup_a and lookup_b");
    const gate = new FileGate(markerDirectory, "lookups");
    // Both tools are inside their Steps at once, so the Steps run in parallel.
    await gate.waitUntilReached(2);
    assert.equal((await sessions.readAgentState(sessionId))?.status, "running_tools");
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "lookup_a -> lookup_a found | lookup_b -> lookup_b found");
    assert.equal(snapshot.usage.modelCalls, 2);
    assert.deepEqual((await executedStepTypes(sessionId)).sort(), [
      "CallTool",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "RunModel",
      "StartAgentSession",
    ]);
  });

  test("a read and an idempotent write finish while a sibling write waits for approval", async () => {
    const sessionId = newSessionId("approval-and-siblings");
    await sessions.startSession(sessionId, "call send_email and refund_order and get_weather");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    await sessions.waitForToolCall(sessionId, "call_1_refund_order", (toolCall) => toolCall.status === "succeeded");
    await sessions.waitForToolCall(sessionId, "call_1_get_weather", (toolCall) => toolCall.status === "succeeded");
    assert.deepEqual(
      (await sessions.getAgentSnapshot(sessionId)).toolCalls.map((toolCall) => [
        toolCall.toolName,
        toolCall.status,
        toolCall.deferralOrder,
      ]),
      [
        ["send_email", "awaiting_approval", 0],
        ["refund_order", "succeeded", 1],
        ["get_weather", "succeeded", 2],
      ],
    );
    assert.deepEqual(toolLog("send_email", sessionId), []);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(toolLog("tool-runs", sessionId).at(-1), `${sessionId} send_email`);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1/call_1_send_email | refund_order -> refund issued | ` +
        "get_weather -> sunny in Paris",
    );
    assert.deepEqual(
      snapshot.messages.map((message) => message.role),
      ["user", "assistant", "assistant", "assistant", "tool", "tool", "tool", "assistant"],
    );
    assert.deepEqual((await executedStepTypes(sessionId)).sort(), [
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "CallTool",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "RunModel",
      "StartAgentSession",
    ]);
  });

  test("a namespaced tool runs under its qualified name", async () => {
    const sessionId = newSessionId("namespace");
    await sessions.startSession(sessionId, "call crm.update_contact");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call crm.update_contact",
      "2 turn-1 assistant function_call crm.update_contact",
      "3 turn-1 tool function_call_result crm.update_contact contact c-1 updated",
      "4 turn-1 assistant crm.update_contact -> contact c-1 updated",
    ]);
    assert.deepEqual(toolLog("crm.update_contact", sessionId), [`${sessionId} call_1_crm_update_contact attempt 1`]);
    const deferredEvent = (await sessions.listAgentEvents(sessionId)).find((event) => event.type === "tool_call_deferred");
    assert.deepEqual(JSON.parse(deferredEvent?.payload ?? "null"), {
      tool_call_id: "call_1_crm_update_contact",
      tool_name: "crm.update_contact",
      effect: "idempotent_write",
      requires_approval: false,
    });
  });

  test("a RunModel that hands off makes two model calls, and the target agent's write gets its own Step", async () => {
    const sessionId = newSessionId("handoff");
    await sessions.startSession(sessionId, "handoff call refund_order");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.state?.lastTurnError, null);
    assert.equal(snapshot.messages.at(-1)?.text, "refund_order -> refund issued");
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} call_1_refund_order attempt 1`]);
    // The first RunModel made the handoff and the billing agent's model call.
    assert.equal(snapshot.usage.modelCalls, 3);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
    const handoffAgents = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "openai_agents.handoff")
      .map((event) => (JSON.parse(event.payload) as { agent_name: string }).agent_name);
    assert.deepEqual(handoffAgents, ["Billing agent"]);
  });

  test("provider reasoning summaries stream as reasoning_delta and reasoning tokens reach usage", async () => {
    const sessionId = newSessionId("reasoning");
    await sessions.startSession(sessionId, "think");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(readTexts(events, "reasoning_delta", "turn-1"), ["weighing the options"]);
    assert.deepEqual(readTexts(events, "text_delta", "turn-1"), ["thought it through"]);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).usage.reasoningTokens, 1);
  });

  test("a model call that fails after a write ran appends the turn failure note, which replays next turn", async () => {
    const sessionId = newSessionId("failed-resume");
    await sessions.startSession(sessionId, "call send_email then fail");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /scripted failure after the tool calls of turn 1/);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call_1_send_email attempt 1`]);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.toolCalls, []);
    const noteText =
      "[Dex] The previous turn failed after these tool calls ran:\n" +
      `- send_email {"to":"ana@example.com"} -> succeeded: email sent for ${sessionId}/turn-1/call_1_send_email\n` +
      "Do not repeat these tool calls unless the user asks again.";
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call send_email then fail",
      `2 turn-1 user ${noteText}`,
    ]);
    assert.deepEqual(JSON.parse(snapshot.messages[1]?.frameworkItem ?? "null"), {
      type: "message",
      role: "user",
      content: noteText,
    });

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const nextState = await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(nextState.lastTurnError, null);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `history | user: call send_email then fail | user: ${noteText}`,
    );
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call_1_send_email attempt 1`]);
  });

  test("a later model call that reuses a finished call ID fails the turn instead of skipping the write", async () => {
    const sessionId = newSessionId("reused-call-id");
    await sessions.startSession(sessionId, "call refund_order then reuse");
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /reuses an earlier tool call ID/);
    // The reused call never got a Step, and the first call ran exactly once.
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} call_1_refund_order attempt 1`]);
    const note = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "";
    assert.match(note, /^\[Dex\] The previous turn failed after these tool calls ran:\n- refund_order /);
  });

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  function toolLog(toolName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${toolName}.log`))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `));
  }
});

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function readTexts(events: readonly AgentEvent[], eventType: string, turnId: string): string[] {
  return events
    .filter((event) => event.type === eventType && event.turnId === turnId)
    .map((event) => (JSON.parse(event.payload) as { text: string }).text);
}

function describeMessage(message: AgentMessage): string {
  const item = JSON.parse(message.frameworkItem) as { type?: string };
  const detail =
    message.text ??
    [item.type, message.toolName, message.toolOutput].filter((part) => part !== null && part !== undefined).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}
