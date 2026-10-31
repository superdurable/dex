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

import type { MastraDBMessage } from "@mastra/core/agent/message-list";
import {
  messageSequenceKey,
  outcomeUnknownToolResult,
  turnFailureNoteFooter,
  turnFailureNoteHeader,
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

import {
  createScriptedMastraAgentFlow,
  type ScriptedMastraAgentFlowOptions,
} from "../fixtures/scripted-mastra-agent.js";

const weatherOutput = '{"forecast":"sunny in Paris"}';
const agentToolNames = [
  "archive_order",
  "audit_export",
  "charge_card",
  "convert_credit",
  "delete_account",
  "get_weather",
  "issue_invoice",
  "lookup_a",
  "lookup_b",
  "lookup_order",
  "refund_order",
  "report_status",
  "schedule_pickup",
  "send_email",
  "set_quota",
  "sync_inventory",
].join(", ");

describe("Mastra agent sessions in Step mode with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let markerDirectory: string;
  const sessionsByFlowType = new Map<string, AgentSessionTestClient>();
  const variants: Record<string, ScriptedMastraAgentFlowOptions> = {
    ScriptedMastraAgent: {},
    ScriptedMastraAgentWithApprovalDeadline: { interruptDeadlineMs: 3_000 },
    ScriptedMastraAgentWithResearcher: { isResearcherAllowed: true },
    ScriptedMastraAgentWithShadowedTool: { isResearcherShadowed: true },
    ScriptedMastraAgentWithMemoryThread: { isMemoryThreadSelected: true },
    ScriptedMastraAgentWithFastClock: { clockSkewMs: 60 * 60 * 1000 },
  };

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flows = Object.entries(variants).map(([flowType, flowOptions]) =>
      createScriptedMastraAgentFlow(markerDirectory, { ...flowOptions, flowType }),
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows,
      isWorkerInProcess: true,
    });
    for (const flow of flows) {
      sessionsByFlowType.set(flow.getFlowType(), new AgentSessionTestClient(environment.client, flow));
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  function sessionsOf(flowType: keyof typeof variants): AgentSessionTestClient {
    const sessions = sessionsByFlowType.get(flowType);
    assert.ok(sessions !== undefined, `no Flow type ${flowType}`);
    return sessions;
  }

  test("a read tool runs as RunModel, CallTool, ResumeTurn, RunModel, and the history replays", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("read-tool");
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const firstSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(firstSnapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather",
      `2 turn-1 assistant get_weather -> ${weatherOutput} | tool call-1-get_weather get_weather ${weatherOutput}`,
    ]);
    assert.deepEqual(toolInvocationStates(firstSnapshot.messages[1]), [["get_weather", "result"]]);
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} call-1-get_weather`]);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(state.lastTurnError, null);
    const secondSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      secondSnapshot.messages.at(-1)?.text,
      `history | user: call get_weather | assistant: get_weather -> ${weatherOutput}`,
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
    const turnOneStepIds = (type: string): string[] =>
      events.filter((event) => event.type === type && event.turnId === "turn-1").map((event) => event.stepExecutionId);
    const modelStepIds = turnOneStepIds("usage_recorded");
    assert.equal(modelStepIds.length, 2);
    assert.notEqual(modelStepIds[0], modelStepIds[1]);
    for (const stepExecutionId of [...modelStepIds, ...turnOneStepIds("text_delta")]) {
      assert.match(stepExecutionId, /RunModel/);
    }
    assert.deepEqual(turnOneStepIds("tool_call_deferred"), [modelStepIds[0]]);
    const toolStepIds = turnOneStepIds("tool_call_started");
    assert.equal(toolStepIds.length, 1);
    assert.match(toolStepIds[0] ?? "", /CallTool/);
    const toolFinished = events.find((event) => event.type === "tool_call_finished");
    assert.deepEqual(JSON.parse(toolFinished?.payload ?? "null"), {
      tool_call_id: "call-1-get_weather",
      tool_name: "get_weather",
      status: "succeeded",
    });
  });

  test("get_agent_snapshot and list_agent_messages return the conversation after a turn", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
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
      lastTurnError: null,
    });
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user hello",
      "2 turn-1 assistant echo: hello",
    ]);
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.equal(snapshot.usage.modelCalls, 1);
    assert.deepEqual(JSON.parse(snapshot.messages[0]?.frameworkItem ?? "null"), {
      role: "user",
      content: { format: 2, parts: [{ type: "text", text: "hello" }], content: "hello" },
    });
    const [storedAnswer] = await environment.client.getAttributes(
      sessionId,
      sessions.flow.agentMessages.instance(messageSequenceKey(2)),
    );
    assert.equal(storedAnswer?.text, "echo: hello");
    assert.equal(decodeFrameworkItem(storedAnswer).content.format, 2);

    const newestPage = await sessions.listAgentMessages(sessionId, null, 1);
    assert.deepEqual(newestPage.messages.map(describeMessage), ["2 turn-1 assistant echo: hello"]);
    assert.equal(newestPage.nextBeforeSequence, 2);
    const olderPage = await sessions.listAgentMessages(sessionId, 2, 0);
    assert.deepEqual(olderPage.messages.map(describeMessage), ["1 turn-1 user hello"]);
    assert.equal(olderPage.nextBeforeSequence, null);
  });

  test("a rejected write never runs and the model sees the rejection message", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("rejected");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(interrupt.interruptId, "call-1-send_email");
    assert.equal(interrupt.arguments, '{"requested_by":"call send_email"}');
    const waitingSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(waitingSnapshot.state?.status, "running_tools");
    assert.deepEqual(
      waitingSnapshot.toolCalls.map((toolCall) => [toolCall.toolCallId, toolCall.effect, toolCall.status]),
      [["call-1-send_email", "unclassified", "awaiting_approval"]],
    );

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not today"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "send_email -> not today");
    assert.equal(snapshot.messages.at(-1)?.toolOutput, "not today");
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
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("resolve-twice");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");

    assert.equal(await sessions.resolveInterrupt(sessionId, "no-such-interrupt", true), false);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1/call-1-send_email`,
    );
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email`]);
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
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("idempotent");
    await sessions.startSession(sessionId, "call refund_order");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      'refund_order -> {"refunded":true}',
    );

    assert.equal(await sessions.sendUserMessage(sessionId, "call delete_account"), true);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "delete_account");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "delete_account -> The tool call failed: account deletion is disabled",
    );
  });

  test("tool calls of one model response run in parallel Steps", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("parallel");
    await sessions.startSession(sessionId, "call lookup_a and lookup_b");
    const gate = new FileGate(markerDirectory, "lookups");
    // Both tools are inside their Steps at once, so the Steps run in parallel.
    await gate.waitUntilReached(2);
    assert.equal((await sessions.readAgentState(sessionId))?.status, "running_tools");
    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, "lookup_a -> A found | lookup_b -> B found");
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

  test("an annotated read runs while a sibling with Mastra requireApproval waits for approval", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("approval-and-read");
    await sessions.startSession(sessionId, "call lookup_order and archive_order");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "archive_order");
    await sessions.waitForToolCall(sessionId, "call-1-lookup_order", (toolCall) => toolCall.status === "succeeded");
    const waitingSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waitingSnapshot.toolCalls.map((toolCall) => [
        toolCall.toolName,
        toolCall.effect,
        toolCall.requiresApproval,
        toolCall.status,
      ]),
      [
        ["lookup_order", "read", false, "succeeded"],
        ["archive_order", "idempotent_write", true, "awaiting_approval"],
      ],
    );
    assert.deepEqual(toolLog("archive_order", sessionId), []);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "lookup_order -> order 42 shipped | archive_order -> order archived",
    );
    assert.deepEqual((await executedStepTypes(sessionId)).sort(), [
      "AwaitToolApproval",
      "CallTool",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "RunModel",
      "StartAgentSession",
    ]);
  });

  test("an approval deadline rejects the call", async () => {
    const sessions = sessionsOf("ScriptedMastraAgentWithApprovalDeadline");
    const sessionId = newSessionId("deadline");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.ok(interrupt.deadline !== null);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "send_email -> approval deadline passed",
    );
    assert.deepEqual(toolLog("send_email", sessionId), []);
  });

  test("a model call that fails after a write ran leaves a note that the next turn's model reads", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("failed-resume");
    await sessions.startSession(sessionId, "call send_email then fail");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /scripted model failure after the tool calls ran/);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email`]);

    const note =
      `${turnFailureNoteHeader}\n` +
      '- send_email {"requested_by":"call send_email then fail"} -> succeeded: ' +
      `email sent for ${sessionId}/turn-1/call-1-send_email\n` +
      turnFailureNoteFooter;
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call send_email then fail",
      `2 turn-1 user ${note}`,
    ]);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `history | user: call send_email then fail | user: ${note}`,
    );
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email`]);
  });

  test("an idempotent write that keeps throwing records outcome_unknown after its attempts", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("exhausted");
    await sessions.startSession(sessionId, "call sync_inventory");
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(toolLog("sync_inventory", sessionId).length, 3);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1);
    assert.equal(answer?.text, `sync_inventory -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(await finishedToolStatuses(sessionId), ["outcome_unknown"]);
    assert.ok((await executedStepTypes(sessionId)).includes("RecordToolFailure"));
  });

  test("a write whose result fails its output schema records outcome_unknown and never runs again", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("output-schema");
    await sessions.startSession(sessionId, "call issue_invoice");
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.deepEqual(toolLog("issue_invoice", sessionId), [`${sessionId} call-1-issue_invoice`]);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1);
    assert.equal(answer?.text, `issue_invoice -> ${outcomeUnknownToolResult}`);
    assert.deepEqual(await finishedToolStatuses(sessionId), ["outcome_unknown"]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "RecordToolOutcomeUnknown",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("AI SDK tools get validated arguments with defaults, and invalid arguments never run them", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("ai-sdk-tools");
    await sessions.startSession(sessionId, "call convert_credit and set_quota and report_status");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "";
    assert.match(answer, /^convert_credit -> credit converted to USD \| set_quota -> The tool call failed: /);
    // An AI SDK tool's error-shaped result is its value, not a Mastra validation error.
    assert.match(answer, / \| report_status -> \{"error":true,"message":"card declined","validationErrors":\{\}\}$/);
    assert.deepEqual(toolLog("convert_credit", sessionId), [`${sessionId} call-1-convert_credit`]);
    assert.deepEqual(toolLog("set_quota", sessionId), []);
    assert.deepEqual(toolLog("report_status", sessionId), [`${sessionId} call-1-report_status`]);
  });

  test("a Mastra tool with a transforming input schema runs once with its transformed input", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("transform");
    await sessions.startSession(sessionId, "call schedule_pickup");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `schedule_pickup -> pickup scheduled for a request of ${"call schedule_pickup".length} characters`,
    );
    assert.deepEqual(toolLog("schedule_pickup", sessionId), [`${sessionId} call-1-schedule_pickup`]);
    assert.deepEqual(await finishedToolStatuses(sessionId), ["succeeded"]);
  });

  test("a Mastra tool whose requestContextSchema rejects the RequestContext fails and never runs", async () => {
    const sessions = sessionsOf("ScriptedMastraAgent");
    const sessionId = newSessionId("request-context-schema");
    await sessions.startSession(sessionId, "call audit_export");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.match(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "",
      /^audit_export -> The tool call failed: Request context validation failed for audit_export/,
    );
    assert.deepEqual(toolLog("audit_export", sessionId), []);
    assert.deepEqual(await finishedToolStatuses(sessionId), ["failed"]);
  });

  test("a sub-agent tool stays hidden and never runs unless its policy is an inline read", async () => {
    const hiddenSessions = sessionsOf("ScriptedMastraAgent");
    const listingSessionId = newSessionId("hidden-list");
    await hiddenSessions.startSession(listingSessionId, "tools");
    await hiddenSessions.waitForCompletedTurns(listingSessionId, 1);
    assert.equal(
      (await hiddenSessions.getAgentSnapshot(listingSessionId)).messages.at(-1)?.text,
      `tools: ${agentToolNames}`,
    );

    const callingSessionId = newSessionId("hidden-call");
    await hiddenSessions.startSession(callingSessionId, "call agent-researcher");
    await hiddenSessions.waitForCompletedTurns(callingSessionId, 1);
    const hiddenAnswer = (await hiddenSessions.getAgentSnapshot(callingSessionId)).messages.at(-1)?.text ?? "";
    assert.match(hiddenAnswer, /^agent-researcher -> .*not found/);
    assert.equal(researcherModelCalls(), 0);

    const allowedSessions = sessionsOf("ScriptedMastraAgentWithResearcher");
    const allowedSessionId = newSessionId("allowed-call");
    await allowedSessions.startSession(allowedSessionId, "tools");
    await allowedSessions.waitForCompletedTurns(allowedSessionId, 1);
    assert.equal(
      (await allowedSessions.getAgentSnapshot(allowedSessionId)).messages.at(-1)?.text,
      `tools: agent-researcher, ${agentToolNames}`,
    );
    assert.equal(await allowedSessions.sendUserMessage(allowedSessionId, "call agent-researcher"), true);
    await allowedSessions.waitForCompletedTurns(allowedSessionId, 2);
    const allowedSnapshot = await allowedSessions.getAgentSnapshot(allowedSessionId);
    assert.match(allowedSnapshot.messages.at(-1)?.text ?? "", /^agent-researcher -> .*echo: research agent-researcher/);
    assert.equal(researcherModelCalls(), 1);
    // Mastra runs the sub-agent inside RunModel and calls the model again there.
    assert.deepEqual(await executedStepTypes(allowedSessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitUserMessage",
      "RunModel",
    ]);
  });

  test("an agent tool that a tool Mastra adds would replace fails the turn instead of running inline", async () => {
    const sessions = sessionsOf("ScriptedMastraAgentWithShadowedTool");
    const sessionId = newSessionId("shadowed");
    await sessions.startSession(sessionId, "call agent-researcher");
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /agent-researcher .* is replaced by a tool Mastra adds itself/);
    assert.deepEqual(toolLog("agent-researcher", sessionId), []);
  });

  test("a RequestContext that selects a Mastra Memory thread fails the turn", async () => {
    const sessions = sessionsOf("ScriptedMastraAgentWithMemoryThread");
    const sessionId = newSessionId("memory-thread");
    await sessions.startSession(sessionId, "hello");
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /must not set mastra__threadId/);
  });

  test("history replays in sequence order even when a Worker clock ran ahead", async () => {
    const sessions = sessionsOf("ScriptedMastraAgentWithFastClock");
    const sessionId = newSessionId("clock-skew");
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, "call send_email"), true);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(interrupt.interruptId, "call-2-send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 3);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.turnId} ${message.role} ${message.text}`),
      [
        "turn-1 user call get_weather",
        `turn-1 assistant get_weather -> ${weatherOutput}`,
        "turn-2 user call send_email",
        `turn-2 assistant send_email -> email sent for ${sessionId}/turn-2/call-2-send_email`,
        "turn-3 user history",
        `turn-3 assistant history | user: call get_weather | assistant: get_weather -> ${weatherOutput} | ` +
          `user: call send_email | assistant: send_email -> email sent for ${sessionId}/turn-2/call-2-send_email`,
      ],
    );
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-2-send_email`]);
  });

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  async function finishedToolStatuses(sessionId: string): Promise<string[]> {
    return (await sessionsOf("ScriptedMastraAgent").listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
  }

  function toolLog(logName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${logName}.log`))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `));
  }

  function researcherModelCalls(): number {
    return new InvocationLog(join(markerDirectory, "model-calls.log"))
      .names()
      .filter((line) => line.startsWith("researcher-model ")).length;
  }
});

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function describeMessage(message: AgentMessage): string {
  const toolDetail =
    message.toolName === null ? "" : ` | tool ${message.toolCallId} ${message.toolName} ${message.toolOutput}`;
  return `${message.sequence} ${message.turnId} ${message.role} ${message.text ?? ""}${toolDetail}`;
}

function decodeFrameworkItem(message: AgentMessage | undefined): MastraDBMessage {
  assert.ok(message !== undefined, "message is missing");
  return JSON.parse(message.frameworkItem) as MastraDBMessage;
}

function toolInvocationStates(message: AgentMessage | undefined): string[][] {
  return decodeFrameworkItem(message).content.parts.flatMap((part) =>
    part.type === "tool-invocation" ? [[part.toolInvocation.toolName, part.toolInvocation.state]] : [],
  );
}
