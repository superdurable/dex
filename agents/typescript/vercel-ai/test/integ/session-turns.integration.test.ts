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

import { hasToolCall, isStepCount, type ModelMessage } from "ai";

import { messageSequenceKey, outcomeUnknownToolResult, type AgentMessage } from "@superdurable/dex-agents";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
  executeCompletedEvent,
  readStepHistory,
} from "@superdurable/dex-agents/testing";

import { createScriptedVercelAiAgentFlow } from "../fixtures/scripted-vercel-ai-agent.js";

describe("Vercel AI SDK agent sessions in Step mode with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let stopAfterRefundSessions: AgentSessionTestClient;
  let twoStepSessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedVercelAiAgentFlow(markerDirectory);
    const stopAfterRefundFlow = createScriptedVercelAiAgentFlow(markerDirectory, {
      flowType: "ScriptedVercelAiAgentStoppingAfterRefund",
      stopWhen: hasToolCall("refund_order"),
    });
    const twoStepFlow = createScriptedVercelAiAgentFlow(markerDirectory, {
      flowType: "ScriptedVercelAiAgentWithTwoModelSteps",
      stopWhen: isStepCount(2),
    });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, stopAfterRefundFlow, twoStepFlow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    stopAfterRefundSessions = new AgentSessionTestClient(environment.client, stopAfterRefundFlow);
    twoStepSessions = new AgentSessionTestClient(environment.client, twoStepFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a read tool runs in CallTool between two RunModel Steps and the history replays on the next turn", async () => {
    const sessionId = newSessionId("read-tool");
    await sessions.startSession(sessionId, "call get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const firstSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(firstSnapshot.messages.map(describeMessage), [
      "1 turn-1 user call get_weather",
      "2 turn-1 assistant tool-call call-1-get_weather get_weather",
      "3 turn-1 tool tool-result call-1-get_weather get_weather sunny in Paris",
      "4 turn-1 assistant get_weather -> sunny in Paris",
    ]);
    assert.equal(firstSnapshot.messages[1]?.toolArguments, '{"requested_by":"call get_weather"}');
    // toModelOutput turned the recorded JSON output into the text the model saw.
    assert.deepEqual((JSON.parse(firstSnapshot.messages[2]?.frameworkItem ?? "null") as ModelMessage).content, [
      {
        type: "tool-result",
        toolCallId: "call-1-get_weather",
        toolName: "get_weather",
        output: { type: "text", value: "sunny in Paris" },
      },
    ]);
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} call-1-get_weather attempt 1`]);
    assert.deepEqual(new InvocationLog(join(markerDirectory, "input-callbacks.log")).names(), []);

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
    assert.equal(modelCallCount(sessionId), 3);

    const events = await sessions.listAgentEvents(sessionId);
    const turnOneEventSteps = (type: string): string[] =>
      events.filter((event) => event.type === type && event.turnId === "turn-1").map((event) => event.stepExecutionId);
    const modelStepIds = turnOneEventSteps("vercel_ai.step_finished");
    assert.equal(modelStepIds.length, 2);
    assert.notEqual(modelStepIds[0], modelStepIds[1]);
    assert.deepEqual(turnOneEventSteps("tool_call_deferred"), [modelStepIds[0]]);
    assert.match(turnOneEventSteps("tool_call_started")[0] ?? "", /CallTool/);
    const stepFinished = events.find((event) => event.type === "vercel_ai.step_finished");
    assert.deepEqual(JSON.parse(stepFinished?.payload ?? "null"), {
      finish_reason: "tool-calls",
      usage: { cached_input_tokens: 2, input_tokens: 10, model_calls: 1, output_tokens: 5, reasoning_tokens: 1 },
    });
    const turnOneText = events
      .filter((event) => event.type === "text_delta" && event.turnId === "turn-1")
      .map((event) => (JSON.parse(event.payload) as { text: string }).text);
    assert.deepEqual(turnOneText, ["get_weather -> sunny in Paris"]);
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

    const [storedUserMessage, storedAnswer] = await environment.client.getAttributes(
      sessionId,
      sessions.flow.agentMessages.instance(messageSequenceKey(1)),
      sessions.flow.agentMessages.instance(messageSequenceKey(2)),
    );
    assert.deepEqual(JSON.parse(storedUserMessage?.frameworkItem ?? "null"), { role: "user", content: "hello" });
    assert.deepEqual(JSON.parse(storedAnswer?.frameworkItem ?? "null"), {
      role: "assistant",
      content: [{ type: "text", text: "echo: hello" }],
    });

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
    assert.equal(snapshot.messages.at(-1)?.text, "send_email -> denied: not today");
    const toolMessage = snapshot.messages.find((message) => message.role === "tool");
    assert.equal(toolMessage?.toolOutput, "not today");
    assert.deepEqual((JSON.parse(toolMessage?.frameworkItem ?? "null") as ModelMessage).content, [
      {
        type: "tool-result",
        toolCallId: "call-1-send_email",
        toolName: "send_email",
        output: { type: "execution-denied", reason: "not today" },
      },
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
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> {"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"}`,
    );
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email attempt 1`]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("an idempotent write runs without approval, needsApproval asks first, and a tool failure reaches the model", async () => {
    const sessionId = newSessionId("idempotent");
    await sessions.startSession(sessionId, "call refund_order");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "refund_order -> refund issued");
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} call-1-refund_order attempt 1`]);

    assert.equal(await sessions.sendUserMessage(sessionId, "call delete_account"), true);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "delete_account");
    const [deleteRecord] = (await sessions.getAgentSnapshot(sessionId)).toolCalls;
    assert.deepEqual([deleteRecord?.effect, deleteRecord?.requiresApproval], ["idempotent_write", true]);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "delete_account -> The tool call failed: account deletion is disabled",
    );
  });

  test("the tool calls of one model response run in parallel tool Steps", async () => {
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

  test("reads run while a sibling write waits for approval, and all results join one tool message", async () => {
    const sessionId = newSessionId("approval-and-reads");
    await sessions.startSession(sessionId, "call send_email and refund_order and get_weather");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    await sessions.waitForToolCall(sessionId, "call-1-get_weather", (toolCall) => toolCall.status === "succeeded");
    await sessions.waitForToolCall(sessionId, "call-1-refund_order", (toolCall) => toolCall.status === "succeeded");
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
      `send_email -> {"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"} | ` +
        "refund_order -> refund issued | get_weather -> sunny in Paris",
    );
    assert.deepEqual(
      snapshot.messages.map((message) => message.role),
      ["user", "assistant", "tool", "assistant"],
    );
    const [, toolCallMessage, toolResultMessage] = snapshot.messages;
    assert.equal(toolCallMessage?.toolCallId, null);
    assert.equal(toolResultMessage?.toolCallId, null);
    const toolResultParts = (JSON.parse(toolResultMessage?.frameworkItem ?? "null") as ModelMessage).content;
    assert.deepEqual(
      Array.isArray(toolResultParts)
        ? toolResultParts.map((part) => ("toolName" in part ? part.toolName : null))
        : [],
      ["send_email", "refund_order", "get_weather"],
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

  test("a turn with two rounds of tool calls keeps every model call's messages", async () => {
    const sessionId = newSessionId("two-rounds");
    await sessions.startSession(sessionId, "call send_email then refund_order");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const sendEmailOutput = `{"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"}`;
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call send_email then refund_order",
      "2 turn-1 assistant tool-call call-1-send_email send_email",
      `3 turn-1 tool tool-result call-1-send_email send_email ${sendEmailOutput}`,
      "4 turn-1 assistant tool-call call-1-refund_order refund_order",
      "5 turn-1 tool tool-result call-1-refund_order refund_order refund issued",
      `6 turn-1 assistant send_email -> ${sendEmailOutput} | refund_order -> refund issued`,
    ]);
    assert.equal(snapshot.usage.modelCalls, 3);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a hasToolCall stop condition ends the turn after the write's result without another model call", async () => {
    const sessionId = newSessionId("stop-when");
    await stopAfterRefundSessions.startSession(sessionId, "call refund_order then get_weather");
    await stopAfterRefundSessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await stopAfterRefundSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user call refund_order then get_weather",
      "2 turn-1 assistant tool-call call-1-refund_order refund_order",
      "3 turn-1 tool tool-result call-1-refund_order refund_order refund issued",
    ]);
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} call-1-refund_order attempt 1`]);
    assert.deepEqual(toolLog("get_weather", sessionId), []);
    assert.equal(modelCallCount(sessionId), 1);
    assert.equal(snapshot.usage.modelCalls, 1);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("isStepCount counts the model steps of every RunModel Step of the turn", async () => {
    const sessionId = newSessionId("step-count");
    await twoStepSessions.startSession(sessionId, "call refund_order then get_weather then lookup_inventory");
    await twoStepSessions.waitForCompletedTurns(sessionId, 1);

    // Counting only the latest RunModel's steps would never reach 2 and would make four model calls.
    assert.equal(modelCallCount(sessionId), 2);
    assert.deepEqual(toolLog("refund_order", sessionId), [`${sessionId} call-1-refund_order attempt 1`]);
    assert.deepEqual(toolLog("get_weather", sessionId), [`${sessionId} call-1-get_weather attempt 1`]);
    assert.deepEqual(toolLog("lookup_inventory", sessionId), []);
    const snapshot = await twoStepSessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.usage.modelCalls, 2);
    assert.deepEqual(
      snapshot.messages.map((message) => [message.role, message.toolName]),
      [
        ["user", null],
        ["assistant", "refund_order"],
        ["tool", "refund_order"],
        ["assistant", "get_weather"],
        ["tool", "get_weather"],
      ],
    );
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a tool call in a response that ends with finish reason length never runs", async () => {
    const sessionId = newSessionId("length");
    await sessions.startSession(sessionId, "truncate refund_order");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      "1 turn-1 user truncate refund_order",
      "2 turn-1 assistant tool-call call-1-refund_order refund_order",
      "3 turn-1 tool tool-result call-1-refund_order refund_order " +
        "The tool call was not run: the model response ended with finish reason length.",
    ]);
    assert.deepEqual(toolLog("refund_order", sessionId), []);
    assert.equal(modelCallCount(sessionId), 1);
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel"]);
    const eventTypes = (await sessions.listAgentEvents(sessionId)).map((event) => event.type);
    assert.ok(!eventTypes.includes("tool_call_deferred"), eventTypes.join(","));

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "history | user: truncate refund_order",
    );
  });

  test("a call with invalid input never runs, and the model sees the validation error in the same RunModel", async () => {
    const sessionId = newSessionId("invalid-input");
    await sessions.startSession(sessionId, "call invalid get_weather");
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => message.role),
      ["user", "assistant", "tool", "assistant"],
    );
    assert.match(snapshot.messages[2]?.toolOutput ?? "", /Invalid input for tool get_weather/);
    assert.match(snapshot.messages.at(-1)?.text ?? "", /^get_weather -> .*Invalid input for tool get_weather/);
    assert.deepEqual(toolLog("get_weather", sessionId), []);
    assert.equal(modelCallCount(sessionId), 2);
    assert.equal(snapshot.usage.modelCalls, 2);
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel"]);
  });

  test("a resumed turn that fails after a write stores the failure note, and the next turn's model sees it", async () => {
    const sessionId = newSessionId("failed-resume");
    await sessions.startSession(sessionId, "call send_email then fail");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /scripted model failure after the tool calls of turn-1/);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email attempt 1`]);

    const failedSnapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(failedSnapshot.toolCalls, []);
    const expectedNote =
      "[Dex] The previous turn failed after these tool calls ran:\n" +
      '- send_email {"requested_by":"call send_email then fail"} -> succeeded: ' +
      `{"delivered":true,"idempotency_key":"${sessionId}/turn-1/call-1-send_email"}\n` +
      "Do not repeat these tool calls unless the user asks again.";
    assert.deepEqual(
      failedSnapshot.messages.map((message) => [message.sequence, message.turnId, message.role, message.text]),
      [
        [1, "turn-1", "user", "call send_email then fail"],
        [2, "turn-1", "user", expectedNote],
      ],
    );
    assert.deepEqual(JSON.parse(failedSnapshot.messages[1]?.frameworkItem ?? "null"), {
      role: "user",
      content: expectedNote,
    });

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text;
    assert.equal(answer, `history | user: call send_email then fail | user: ${expectedNote}`);
    assert.deepEqual(toolLog("send_email", sessionId), [`${sessionId} call-1-send_email attempt 1`]);
  });

  test("a plain error from an idempotent write exhausts CallTool retries and the model sees outcome_unknown", async () => {
    const sessionId = newSessionId("exhausted");
    await sessions.startSession(sessionId, "call sync_inventory");
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.deepEqual(toolLog("sync_inventory", sessionId), [
      `${sessionId} call-1-sync_inventory attempt 1`,
      `${sessionId} call-1-sync_inventory attempt 2`,
      `${sessionId} call-1-sync_inventory attempt 3`,
    ]);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.find((message) => message.role === "tool")?.toolOutput, outcomeUnknownToolResult);
    assert.equal(snapshot.messages.at(-1)?.text, `sync_inventory -> ${outcomeUnknownToolResult}`);
    const finishedStatuses = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "tool_call_finished")
      .map((event) => (JSON.parse(event.payload) as { status: string }).status);
    assert.deepEqual(finishedStatuses, ["outcome_unknown"]);
  });

  test("outputs JSON cannot hold are recorded instead of failing after the tool ran", async () => {
    const sessionId = newSessionId("unserializable");
    await sessions.startSession(sessionId, "call count_records and describe_graph");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.deepEqual(toolLog("count_records", sessionId), [`${sessionId} call-1-count_records attempt 1`]);
    assert.deepEqual(toolLog("describe_graph", sessionId), [`${sessionId} call-1-describe_graph attempt 1`]);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text ?? "";
    assert.ok(answer.startsWith('count_records -> {"total":"12345678901234567890"} | describe_graph -> '), answer);
    assert.match(answer, /describe_graph -> <ref \*1> \{ name: 'graph', self: \[Circular \*1\] \}$/);
  });

  test("trusted MCP annotations make a tool a read, and reasoning streams as reasoning deltas", async () => {
    const sessionId = newSessionId("annotations");
    await sessions.startSession(sessionId, "call lookup_inventory");
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.deepEqual(toolLog("lookup_inventory", sessionId), [`${sessionId} call-1-lookup_inventory attempt 1`]);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, 'lookup_inventory -> {"in_stock":3}');
    const deferred = (await sessions.listAgentEvents(sessionId)).find((event) => event.type === "tool_call_deferred");
    assert.deepEqual(JSON.parse(deferred?.payload ?? "null"), {
      effect: "read",
      requires_approval: false,
      tool_call_id: "call-1-lookup_inventory",
      tool_name: "lookup_inventory",
    });

    assert.equal(await sessions.sendUserMessage(sessionId, "think"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const answer = (await sessions.getAgentSnapshot(sessionId)).messages.at(-1);
    assert.equal(answer?.text, "thought done");
    assert.deepEqual(JSON.parse(answer?.frameworkItem ?? "null"), {
      role: "assistant",
      content: [
        { type: "reasoning", text: "weighing the request" },
        { type: "text", text: "thought done" },
      ],
    });
    const deltas = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.turnId === "turn-2" && event.type.endsWith("_delta"))
      .map((event) => [event.type, (JSON.parse(event.payload) as { text: string }).text]);
    assert.deepEqual(deltas, [
      ["reasoning_delta", "weighing the request"],
      ["text_delta", "thought done"],
    ]);
  });

  function toolLog(toolName: string, sessionId: string): string[] {
    return new InvocationLog(join(markerDirectory, `${toolName}.log`))
      .names()
      .filter((line) => line.startsWith(`${sessionId} `));
  }

  function modelCallCount(sessionId: string): number {
    return toolLog("model-calls", sessionId).length;
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }
});

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

function describeMessage(message: AgentMessage): string {
  const item = JSON.parse(message.frameworkItem) as ModelMessage;
  const partType =
    Array.isArray(item.content) && message.text === null ? item.content.map((part) => part.type).join(",") : null;
  const detail =
    message.text ??
    [partType, message.toolCallId, message.toolName, message.toolOutput].filter((part) => part !== null).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}
