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
  createAgentSessionFlow,
  emptyAgentUsage,
  messageSequenceKey,
  outcomeUnknownToolResult,
  type AgentFrameworkRunner,
  type AgentMessage,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type FrameworkMessage,
  type ToolExecutionResult,
} from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
  waitForCompletedEvent,
} from "../../src/testing/index.js";
import { createScriptedAgentFlow } from "../fixtures/scripted-agent-runner.js";

describe("agent session turns with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let deadlineSessions: AgentSessionTestClient;
  let longDeadlineSessions: AgentSessionTestClient;
  let askUserSessions: AgentSessionTestClient;
  let malformedBuilderSessions: AgentSessionTestClient;
  let missingBuilderSessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedAgentFlow(markerDirectory);
    const deadlineFlow = createScriptedAgentFlow(markerDirectory, {
      flowType: "ScriptedAgentWithApprovalDeadline",
      interruptDeadlineMs: 3_000,
    });
    const longDeadlineFlow = createScriptedAgentFlow(markerDirectory, {
      flowType: "ScriptedAgentWithLongApprovalDeadline",
      interruptDeadlineMs: 60_000,
    });
    const askUserFlow = createScriptedAgentFlow(markerDirectory, {
      flowType: "ScriptedAgentWithAskUser",
      builtInTools: ["ask_user"],
    });
    const malformedBuilderFlow = createScriptedAgentFlow(markerDirectory, {
      flowType: "ScriptedAgentWithMalformedUnfinishedMessages",
      unfinishedTurnMessages: "malformed",
    });
    const missingBuilderFlow = createAgentSessionFlow(
      new IncrementalRunnerWithoutBuilder(join(markerDirectory, "incremental-model-calls.log")),
      "IncrementalRunnerWithoutBuilder",
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, deadlineFlow, longDeadlineFlow, askUserFlow, malformedBuilderFlow, missingBuilderFlow],
      isWorkerInProcess: true,
    });
    longDeadlineSessions = new AgentSessionTestClient(environment.client, longDeadlineFlow);
    sessions = new AgentSessionTestClient(environment.client, flow);
    deadlineSessions = new AgentSessionTestClient(environment.client, deadlineFlow);
    askUserSessions = new AgentSessionTestClient(environment.client, askUserFlow);
    malformedBuilderSessions = new AgentSessionTestClient(environment.client, malformedBuilderFlow);
    missingBuilderSessions = new AgentSessionTestClient(environment.client, missingBuilderFlow);
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
      "2 turn-1 assistant tool_call get_weather",
      "3 turn-1 tool tool_result get_weather sunny in Paris",
      "4 turn-1 assistant get_weather -> sunny in Paris",
    ]);
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} turn-1-get_weather attempt 1`]);
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

    const events = await sessions.listAgentEvents(sessionId);
    const turnStarted = events.find((event) => event.type === "turn_started" && event.turnId === "turn-2");
    assert.deepEqual(JSON.parse(turnStarted?.payload ?? "null"), { sequence: 5 });
    const turnCompleted = events.find((event) => event.type === "turn_completed" && event.turnId === "turn-1");
    assert.deepEqual(JSON.parse(turnCompleted?.payload ?? "null"), { message_count: 3 });
    const lastUsage = events.filter((event) => event.type === "usage_recorded").at(-1);
    assert.equal(
      lastUsage?.payload,
      '{"recorded":{"cached_input_tokens":0,"input_tokens":10,"model_calls":1,"output_tokens":5,"reasoning_tokens":0},' +
        '"session":{"cached_input_tokens":0,"input_tokens":30,"model_calls":3,"output_tokens":15,"reasoning_tokens":0}}',
    );
    const turnOneSteps = (type: string): string[] =>
      events
        .filter((event) => event.type === type && event.turnId === "turn-1")
        .map((event) => event.stepExecutionId);
    const modelStepIds = turnOneSteps("usage_recorded");
    assert.equal(modelStepIds.length, 2);
    assert.notEqual(modelStepIds[0], modelStepIds[1]);
    for (const stepExecutionId of modelStepIds) {
      assert.match(stepExecutionId, /RunModel/);
    }
    const toolStepIds = turnOneSteps("tool_call_started");
    assert.equal(toolStepIds.length, 1);
    assert.match(toolStepIds[0] ?? "", /CallTool/);
    assert.deepEqual(turnOneSteps("tool_call_deferred"), [modelStepIds[0]]);
    const executedSteps = (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
    assert.deepEqual(executedSteps, [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "AwaitUserMessage",
      "RunModel",
    ]);
    const eventTypes = events.map((event) => event.type);
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

    const [storedAnswer] = await environment.client.getAttributes(
      sessionId,
      sessions.flow.agentMessages.instance(messageSequenceKey(2)),
    );
    assert.equal(storedAnswer?.text, "echo: hello");

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
    assert.equal(waitingState?.status, "running_tools");
    const waitingSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waitingSnapshot.toolCalls.map((toolCall) => [
        toolCall.toolCallId,
        toolCall.effect,
        toolCall.status,
        toolCall.deferralOrder,
      ]),
      [["turn-1-send_email", "unclassified", "awaiting_approval", 0]],
    );

    assert.equal(
      await sessions.resolveInterrupt(sessionId, interrupt.interruptId, false, "not today"),
      true,
    );
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "send_email -> not today",
    );
    assert.deepEqual(toolLog("send_email", sessionId), []);
    const eventTypes = (await sessions.listAgentEvents(sessionId)).map((event) => event.type);
    assert.ok(eventTypes.includes("tool_call_deferred"));
    assert.ok(eventTypes.includes("interrupt_raised"));
    assert.ok(eventTypes.includes("interrupt_resolved"));
    assert.ok(eventTypes.includes("tool_call_finished"));
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
      `send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email`,
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

  test("tool calls of one model response run in parallel Steps", async () => {
    const sessionId = newSessionId("parallel");
    await sessions.startSession(sessionId, "call lookup_a and lookup_b");
    const gate = new FileGate(markerDirectory, "lookups");
    // Both tools are inside their Steps at once, so the Steps run in parallel.
    await gate.waitUntilReached(2);
    const runningState = await sessions.readAgentState(sessionId);
    assert.equal(runningState?.status, "running_tools");
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

  test("a read tool runs while a sibling write waits for approval", async () => {
    const sessionId = newSessionId("approval-and-read");
    await sessions.startSession(sessionId, "call send_email and get_weather and refund_order");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    await sessions.waitForToolCall(sessionId, "turn-1-get_weather", (toolCall) => toolCall.status === "succeeded");
    await sessions.waitForToolCall(sessionId, "turn-1-refund_order", (toolCall) => toolCall.status === "succeeded");
    const waitingSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waitingSnapshot.toolCalls.map((toolCall) => [toolCall.toolName, toolCall.status, toolCall.deferralOrder]),
      [
        ["send_email", "awaiting_approval", 0],
        ["get_weather", "succeeded", 1],
        ["refund_order", "succeeded", 2],
      ],
    );
    assert.deepEqual(toolLog("send_email", sessionId), []);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const toolRuns = toolLog("tool-runs", sessionId);
    assert.equal(toolRuns.at(-1), `${sessionId} send_email`);
    assert.deepEqual([...toolRuns].sort(), [
      `${sessionId} get_weather`,
      `${sessionId} refund_order`,
      `${sessionId} send_email`,
    ]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email | ` +
        "get_weather -> sunny in Paris | refund_order -> refund issued",
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

  test("a second agent Flow type rejects an approval whose deadline passed", async () => {
    const sessionId = newSessionId("deadline");
    await deadlineSessions.startSession(sessionId, "call send_email");
    const interrupt = await deadlineSessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.ok(interrupt.deadline !== null);
    assert.equal(Date.parse(interrupt.deadline) - Date.parse(interrupt.createdAt), 3_000);

    await deadlineSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await deadlineSessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    assert.deepEqual(await approvalWaitAttempts(sessionId), [1]);
    const snapshot = await deadlineSessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "send_email -> approval deadline passed",
    );
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.deepEqual(toolLog("send_email", sessionId), []);
    const resolvedEvent = (await deadlineSessions.listAgentEvents(sessionId)).find(
      (event) => event.type === "interrupt_resolved",
    );
    assert.deepEqual(JSON.parse(resolvedEvent?.payload ?? "null"), {
      interrupt_id: "turn-1-send_email",
      kind: "tool_approval",
      approved: false,
      answered: false,
      rejection_message: "approval deadline passed",
      cancellation_reason: null,
    });
  });

  test("a model call that fails after a write ran appends the turn failure note and never repeats the write", async () => {
    const sessionId = newSessionId("failed-resume");
    await sessions.startSession(sessionId, "call send_email then fail");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /scripted failure after the tool calls of turn-1/);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} turn-1-send_email attempt 1`]);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.deepEqual(
      snapshot.messages.map((message) => [message.sequence, message.turnId, message.role]),
      [
        [1, "turn-1", "user"],
        [2, "turn-1", "user"],
      ],
    );
    assert.equal(
      snapshot.messages[1]?.text,
      "[Dex] The previous turn failed after these tool calls ran:\n" +
        `- send_email {"requested_by":"call send_email then fail"} -> succeeded: ` +
        `email sent for ${sessionId}/turn-1/turn-1-send_email\n` +
        "Do not repeat these tool calls unless the user asks again.",
    );
    assert.deepEqual(
      (await sessions.listAgentEvents(sessionId))
        .filter((event) => event.type === "turn_failed")
        .map((event) => event.turnId),
      ["turn-1"],
    );

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "";
    assert.ok(answer.includes("user: [Dex] The previous turn failed"), answer);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} turn-1-send_email attempt 1`]);
  });

  test("exhausted CallTool retries record outcome_unknown for a write and failed for a read", async () => {
    const sessionId = newSessionId("exhausted");
    await sessions.startSession(sessionId, "call sync_inventory and check_stock");
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    for (const toolName of ["sync_inventory", "check_stock"]) {
      assert.deepEqual(toolLog(toolName, sessionId), [
        `${sessionId} turn-1-${toolName} attempt 1`,
        `${sessionId} turn-1-${toolName} attempt 2`,
        `${sessionId} turn-1-${toolName} attempt 3`,
      ]);
    }
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const toolOutputs = snapshot.messages
      .filter((message) => message.role === "tool")
      .map((message) => [message.toolName, message.toolOutput]);
    assert.equal(toolOutputs.length, 2);
    assert.deepEqual(toolOutputs[0], ["sync_inventory", outcomeUnknownToolResult]);
    assert.equal(toolOutputs[1]?.[0], "check_stock");
    assert.match(toolOutputs[1]?.[1] ?? "", /^The tool call failed: .*stock service unavailable/);
    const finished = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => JSON.parse(event.payload) as { tool_name: string; status: string });
    assert.deepEqual(
      finished.map((payload) => `${payload.tool_name} ${payload.status}`).sort(),
      ["check_stock failed", "sync_inventory outcome_unknown"],
    );
  });

  test("send_user_message during a turn queues the message for the next turn", async () => {
    const sessionId = newSessionId("queued");
    await sessions.startSession(sessionId, "slow");
    const gate = new FileGate(markerDirectory, "turn");
    await gate.waitUntilReached();
    assert.equal(await sessions.sendUserMessage(sessionId, "second"), true);
    const runningSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(runningSnapshot.state?.status, "running_model");
    assert.equal(runningSnapshot.queuedMessageCount, 1);
    assert.deepEqual(runningSnapshot.messages.map((message) => message.text), ["slow"]);

    gate.release();
    await sessions.waitForCompletedTurns(sessionId, 2);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => `${message.turnId} ${message.text}`),
      ["turn-1 slow", "turn-1 slow turn done", "turn-2 second", "turn-2 echo: second"],
    );
    assert.equal(snapshot.queuedMessageCount, 0);
  });

  test("an approval with a 60 second deadline resolves at once, on the first WaitFor attempt", async () => {
    const sessionId = newSessionId("long-deadline");
    await longDeadlineSessions.startSession(sessionId, "call send_email");
    const interrupt = await longDeadlineSessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(Date.parse(interrupt.deadline ?? "") - Date.parse(interrupt.createdAt), 60_000);
    const approvedAt = Date.now();
    assert.equal(
      await longDeadlineSessions.resolveInterrupt(sessionId, interrupt.interruptId, true),
      true,
    );
    await longDeadlineSessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 10_000 });
    assert.ok(Date.now() - approvedAt < 5_000, `turn took ${Date.now() - approvedAt} ms after approval`);
    assert.deepEqual(await approvalWaitAttempts(sessionId), [1]);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} turn-1-send_email attempt 1`]);
  });

  test("a turn with two tool batches offsets deferral order and resumes after each batch", async () => {
    const sessionId = newSessionId("two-batches");
    await sessions.startSession(
      sessionId,
      "call get_weather and refund_order, then call send_email and get_weather",
    );
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(interrupt.interruptId, "turn-1-b1-send_email");
    await sessions.waitForToolCall(sessionId, "turn-1-b1-get_weather", (toolCall) => toolCall.status === "succeeded");
    const waitingSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waitingSnapshot.toolCalls.map((toolCall) => [toolCall.toolCallId, toolCall.status, toolCall.deferralOrder]),
      [
        ["turn-1-get_weather", "succeeded", 0],
        ["turn-1-refund_order", "succeeded", 1],
        ["turn-1-b1-send_email", "awaiting_approval", 2],
        ["turn-1-b1-get_weather", "succeeded", 3],
      ],
    );
    const [runState] = await environment.client.getAttributes(sessionId, sessions.flow.frameworkRunState);
    assert.deepEqual((JSON.parse(runState ?? "null") as { pendingToolCallIds: string[] }).pendingToolCallIds, [
      "turn-1-b1-send_email",
      "turn-1-b1-get_weather",
    ]);

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "get_weather -> sunny in Paris | refund_order -> refund issued | " +
        `send_email -> email sent for ${sessionId}/turn-1/turn-1-b1-send_email | get_weather -> sunny in Paris`,
    );
    assert.equal(snapshot.usage.modelCalls, 3);
    assert.deepEqual(snapshot.toolCalls, []);
    const [clearedRunState] = await environment.client.getAttributes(sessionId, sessions.flow.frameworkRunState);
    assert.equal(clearedRunState, undefined);
    assert.deepEqual(toolLog("get_weather", sessionId), [
      `${sessionId} turn-1-get_weather attempt 1`,
      `${sessionId} turn-1-b1-get_weather attempt 1`,
    ]);
    const executed = (await executedStepTypes(sessionId)).filter((stepType) => stepType !== "StartAgentSession");
    assert.deepEqual(executed.filter((stepType) => stepType === "RunModel").length, 3);
    assert.deepEqual(executed.filter((stepType) => stepType === "ResumeTurn").length, 2);
    assert.deepEqual(executed.filter((stepType) => stepType === "CallTool").length, 3);
    assert.deepEqual(executed.filter((stepType) => stepType === "CallSingleAttemptTool").length, 1);
    assert.equal(executed.at(-1), "RunModel");
  });

  test("a turn that fails after two batches lists both batches in the note, and a reused ID is rejected", async () => {
    const sessionId = newSessionId("two-batches-fail");
    await sessions.startSession(sessionId, "call refund_order, then call send_email, then repeat");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /tool call turn-1-refund_order was already requested in turn-1/);
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} turn-1-refund_order attempt 1`]);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} turn-1-b1-send_email attempt 1`]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      "[Dex] The previous turn failed after these tool calls ran:\n" +
        `- refund_order {"requested_by":"call refund_order, then call send_email, then repeat"} -> succeeded: refund issued\n` +
        `- send_email {"requested_by":"call refund_order, then call send_email, then repeat"} -> succeeded: ` +
        `email sent for ${sessionId}/turn-1/turn-1-b1-send_email\n` +
        "Do not repeat these tool calls unless the user asks again.",
    );
  });

  test("with two interrupts, one is resolved and the deadline then rejects the other", async () => {
    const sessionId = newSessionId("two-interrupts");
    await deadlineSessions.startSession(sessionId, "call send_email and charge_card");
    const emailInterrupt = await deadlineSessions.waitForPendingInterrupt(sessionId, "send_email");
    await deadlineSessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(
      await deadlineSessions.resolveInterrupt(sessionId, emailInterrupt.interruptId, true),
      true,
    );
    await deadlineSessions.waitForCompletedTurns(sessionId, 1);

    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} turn-1-send_email attempt 1`]);
    assert.deepEqual(toolLog("charge_card", sessionId), []);
    const snapshot = await deadlineSessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email | ` +
        "charge_card -> approval deadline passed",
    );
    assert.deepEqual(await approvalWaitAttempts(sessionId), [1, 1]);
    const resolutions = (await deadlineSessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "interrupt_resolved")
      .map((event) => JSON.parse(event.payload) as Record<string, unknown>);
    assert.deepEqual(resolutions, [
      {
        interrupt_id: "turn-1-send_email",
        kind: "tool_approval",
        approved: true,
        answered: false,
        rejection_message: null,
        cancellation_reason: null,
      },
      {
        interrupt_id: "turn-1-charge_card",
        kind: "tool_approval",
        approved: false,
        answered: false,
        rejection_message: "approval deadline passed",
        cancellation_reason: null,
      },
    ]);
  });

  test("a tool that asks for input raises a question and runs again with the answer and its continuation", async () => {
    new FileGate(markerDirectory, "confirm_shipment").release();
    const sessionId = newSessionId("tool-input");
    await sessions.startSession(sessionId, "call confirm_shipment");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "confirm_shipment");
    assert.deepEqual(
      [interrupt.interruptId, interrupt.kind, interrupt.question, interrupt.choices, interrupt.allowsFreeText],
      ["turn-1-confirm_shipment-input-1", "question", "Ship now?", ["yes", "no"], false],
    );
    const waiting = await sessions.readToolCall(sessionId, "turn-1-confirm_shipment");
    assert.deepEqual(
      [
        waiting?.status,
        waiting?.inputContinuation,
        waiting?.inputQuestion,
        waiting?.inputChoices,
        waiting?.inputAllowsFreeText,
        waiting?.inputAnswers,
      ],
      ["awaiting_answer", "continuation-turn-1-confirm_shipment", "Ship now?", ["yes", "no"], false, []],
    );

    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "maybe"), false);
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "yes"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "confirm_shipment -> shipped after yes");
    assert.deepEqual(toolLog("confirm_shipment", sessionId), [
      `${sessionId} turn-1-confirm_shipment attempt 1`,
      `${sessionId} turn-1-confirm_shipment attempt 1`,
    ]);
    assert.deepEqual(toolLog("confirm_shipment-answers", sessionId), [
      `${sessionId} turn-1-confirm_shipment-input-1=yes (Ship now?) continuation continuation-turn-1-confirm_shipment`,
    ]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallSingleAttemptTool",
      // The first AwaitQuestionAnswer raises the question under the lock; the second takes the answer.
      "AwaitQuestionAnswer",
      "AwaitQuestionAnswer",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("two tools that ask at once raise two questions, and a sibling approval resolved meanwhile stays resolved", async () => {
    new FileGate(markerDirectory, "confirm_shipment").release();
    const sessionId = newSessionId("two-tool-questions");
    await sessions.startSession(sessionId, "call confirm_shipment and confirm_return");
    const waiting = await sessions.waitForAgentSnapshot(
      sessionId,
      (snapshot) => snapshot.pendingInterrupts.length === 2,
      "two pending tool questions",
    );
    assert.deepEqual(
      waiting.pendingInterrupts.map((interrupt) => [interrupt.interruptId, interrupt.kind, interrupt.question]).sort(),
      [
        ["turn-1-confirm_return-input-1", "question", "Return now?"],
        ["turn-1-confirm_shipment-input-1", "question", "Ship now?"],
      ],
    );
    assert.deepEqual(
      waiting.toolCalls.map((toolCall) => [toolCall.toolName, toolCall.status]),
      [
        ["confirm_shipment", "awaiting_answer"],
        ["confirm_return", "awaiting_answer"],
      ],
    );
    assert.equal(await sessions.answerQuestion(sessionId, "turn-1-confirm_return-input-1", "no"), true);
    assert.deepEqual(
      (await sessions.readPendingInterrupts(sessionId)).map((interrupt) => interrupt.interruptId),
      ["turn-1-confirm_shipment-input-1"],
    );
    assert.equal(await sessions.answerQuestion(sessionId, "turn-1-confirm_shipment-input-1", "yes"), true);
    assert.equal(await sessions.answerQuestion(sessionId, "turn-1-confirm_shipment-input-1", "no"), false);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "confirm_shipment -> shipped after yes | confirm_return -> confirm_return confirmed: no",
    );

    const raceSessionId = newSessionId("approval-while-tool-runs");
    await sessions.startSession(raceSessionId, "call send_email and gated_confirm");
    const approval = await sessions.waitForPendingInterrupt(raceSessionId, "send_email");
    const gate = new FileGate(markerDirectory, "gated_confirm");
    await gate.waitUntilReached();
    // The tool Step loaded PendingInterrupts with this approval still pending.
    assert.equal(await sessions.resolveInterrupt(raceSessionId, approval.interruptId, true), true);
    gate.release();
    await sessions.waitForPendingInterrupt(raceSessionId, "gated_confirm");
    assert.deepEqual(
      (await sessions.readPendingInterrupts(raceSessionId)).map((interrupt) => interrupt.interruptId),
      ["turn-1-gated_confirm-input-1"],
    );
    assert.equal(await sessions.answerQuestion(raceSessionId, "turn-1-gated_confirm-input-1", "yes"), true);
    await sessions.waitForCompletedTurns(raceSessionId, 1);
    assert.deepEqual((await sessions.getAgentSnapshot(raceSessionId)).pendingInterrupts, []);
    assert.deepEqual(toolLog("send_email", raceSessionId), [`${raceSessionId} turn-1-send_email attempt 1`]);
    assert.deepEqual(toolLog("gated_confirm", raceSessionId), [
      `${raceSessionId} turn-1-gated_confirm attempt 1`,
      `${raceSessionId} turn-1-gated_confirm attempt 1`,
    ]);
  });

  test("a framework runner that exposes ask_user gets the answer as the tool result", async () => {
    const sessionId = newSessionId("adapter-ask-user");
    await askUserSessions.startSession(sessionId, "call ask_user");
    const question = await askUserSessions.waitForPendingInterrupt(sessionId, "ask_user");
    assert.deepEqual(
      [question.interruptId, question.kind, question.question, question.choices, question.allowsFreeText],
      ["turn-1-ask_user", "question", "Proceed?", ["yes", "no"], true],
    );
    assert.equal(await askUserSessions.answerQuestion(sessionId, question.interruptId, "yes"), true);
    await askUserSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await askUserSessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      'ask_user -> {"status":"answered","answer":"yes"}',
    );
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitQuestionAnswer",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a tool question whose interrupt ID is another call's ID fails the asking call clearly", async () => {
    const sessionId = newSessionId("interrupt-collision");
    // The second call's ID, turn-1-confirm_shipment-input-1, is the first call's question ID.
    await sessions.startSession(sessionId, "call confirm_shipment and confirm_shipment-input-1");
    const approval = await sessions.waitForPendingInterrupt(sessionId, "confirm_shipment-input-1");
    assert.equal(approval.interruptId, "turn-1-confirm_shipment-input-1");
    const failed = await sessions.waitForToolCall(sessionId, "turn-1-confirm_shipment", (toolCall) =>
      toolCall.status === "failed",
    );
    assert.equal(
      failed.error,
      "the tool's question cannot use interrupt ID turn-1-confirm_shipment-input-1, which another tool call of this turn uses",
    );
    assert.deepEqual(
      (await sessions.readPendingInterrupts(sessionId)).map((interrupt) => [interrupt.interruptId, interrupt.kind]),
      [["turn-1-confirm_shipment-input-1", "tool_approval"]],
    );
    assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, false, "not this one"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "confirm_shipment -> The tool call failed: the tool's question cannot use interrupt ID " +
        "turn-1-confirm_shipment-input-1, which another tool call of this turn uses | " +
        "confirm_shipment-input-1 -> not this one",
    );
    assert.deepEqual(toolLog("confirm_shipment", sessionId), [`${sessionId} turn-1-confirm_shipment attempt 1`]);
  });

  test("a malformed unfinished-turn result never blocks RecordTurnFailure, which stores the note", async () => {
    const sessionId = newSessionId("malformed-builder");
    await malformedBuilderSessions.startSession(sessionId, "call refund_order then fail");
    const state = await malformedBuilderSessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /scripted failure after the tool calls of turn-1/);
    const snapshot = await malformedBuilderSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => [message.sequence, message.role]),
      [
        [1, "user"],
        [2, "user"],
      ],
    );
    assert.match(snapshot.messages[1]?.text ?? "", /^\[Dex\] The previous turn failed after these tool calls ran:/);
  });

  test("new messages without a builder fail the turn after one model call", async () => {
    const sessionId = newSessionId("missing-builder");
    await missingBuilderSessions.startSession(sessionId, "store as you go");
    const state = await missingBuilderSessions.waitForCompletedTurns(sessionId, 1);
    const expectedError =
      "a runner that sets ToolCallsRequested.newMessages must implement buildUnfinishedTurnMessages";
    assert.equal(state.lastTurnError, expectedError);
    assert.deepEqual(
      new InvocationLog(join(markerDirectory, "incremental-model-calls.log")).names().filter((line) => line === sessionId),
      [sessionId],
    );
    const snapshot = await missingBuilderSessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.usage.modelCalls, 1);
    assert.deepEqual(snapshot.messages.map((message) => message.role), ["user"]);
    assert.deepEqual(snapshot.toolCalls, []);
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel"]);
    const failed = (await missingBuilderSessions.listAgentEvents(sessionId)).find((event) => event.type === "turn_failed");
    assert.deepEqual(JSON.parse(failed?.payload ?? "null"), { error: expectedError });
  });

  /** Final attempts of every completed AwaitToolApproval WaitFor; 1 means no WaitFor failure was retried. */
  async function approvalWaitAttempts(sessionId: string): Promise<number[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === waitForCompletedEvent && entry.stepType === "AwaitToolApproval")
      .map((entry) => entry.finalAttempt);
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }

  function toolLog(toolName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${toolName}.log`)).names().filter((line) =>
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

/** Sets `newMessages` but lacks `buildUnfinishedTurnMessages`, a runner defect the session catches. */
class IncrementalRunnerWithoutBuilder implements AgentFrameworkRunner {
  public constructor(private readonly modelCallLogPath: string) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return { role: "user", text, toolCallId: null, toolName: null, toolArguments: null, toolOutput: null,
      frameworkItem: JSON.stringify({ text }) };
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    new InvocationLog(this.modelCallLogPath).record(turn.flowId);
    const toolCallMessage: FrameworkMessage = {
      role: "assistant",
      text: null,
      toolCallId: "call-1",
      toolName: "lookup",
      toolArguments: "{}",
      toolOutput: null,
      frameworkItem: JSON.stringify({ call: "lookup" }),
    };
    return {
      kind: "tool_calls_requested",
      frameworkRunState: "{}",
      newMessages: [toolCallMessage],
      toolCalls: [{ toolCallId: "call-1", toolName: "lookup", arguments: "{}", policy: { effect: "read", requiresApproval: false } }],
      usage: { ...emptyAgentUsage(), modelCalls: 1 },
    };
  }

  public async executeDeferredToolCall(): Promise<ToolExecutionResult> {
    return { output: "unreachable" };
  }
}
