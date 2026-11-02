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

import { StepExecutionId, TimerId } from "@superdurable/dex";
import { MockLanguageModelV4 } from "ai/test";

import { createAiSdkModelClient } from "../../src/ai-sdk/index.js";
import { ToolExecutionError, createAgentSessionFlow, type AgentEvent, type AgentMessage } from "../../src/index.js";
import { StepwiseAgentRunner, createStepwiseAgentSessionFlow, functionTool } from "../../src/stepwise/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
  ScriptedModelClient,
  executeCompletedEvent,
  readStepHistory,
} from "../../src/testing/index.js";
import {
  createStepwiseScriptedLanguageModel,
  readLoggedModelCalls,
  type LoggedModelCall,
} from "../fixtures/stepwise-scripted-language-model.js";
import { createStepwiseTestAgentFlow } from "../fixtures/stepwise-test-agent.js";

describe("Stepwise agent turns with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let deadlineSessions: AgentSessionTestClient;
  let limitedSessions: AgentSessionTestClient;
  let sessionPolicySessions: AgentSessionTestClient;
  let builtInClashSessions: AgentSessionTestClient;
  let scriptedClientSessions: AgentSessionTestClient;
  let markerDirectory: string;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createStepwiseTestAgentFlow(markerDirectory);
    const deadlineFlow = createStepwiseTestAgentFlow(markerDirectory, {
      flowType: "StepwiseTestAgentWithQuestionDeadline",
      interruptDeadlineMs: 3_000,
    });
    const limitedFlow = createStepwiseTestAgentFlow(markerDirectory, {
      flowType: "StepwiseTestAgentWithTwoModelCalls",
      maxModelCallsPerTurn: 2,
    });
    const sessionPolicyFlow = createStepwiseTestAgentFlow(markerDirectory, {
      flowType: "StepwiseTestAgentWithSessionPolicies",
      sessionToolPolicies: { charge_card: { effect: "non_idempotent_write", requiresApproval: true } },
    });
    const builtInClashFlow = createAgentSessionFlow(
      new StepwiseAgentRunner({
        name: "built-in-clash",
        instructions: "",
        model: createAiSdkModelClient(createStepwiseScriptedLanguageModel(markerDirectory)),
        tools: [
          functionTool({
            name: "ask_user",
            description: "An application tool named like a built-in tool.",
            parameters: { type: "object" },
            effect: "read",
            execute: () => "asked",
          }),
        ],
      }),
      "StepwiseBuiltInClashAgent",
      { builtInTools: ["ask_user"] },
    );
    const scriptedClientFlow = createStepwiseAgentSessionFlow(
      {
        name: "scripted-client",
        instructions: "Help.",
        model: new ScriptedModelClient((request) =>
          ScriptedModelClient.toolResults(request).length > 0
            ? { text: `done: ${ScriptedModelClient.toolResults(request).map((result) => result.output).join(", ")}` }
            : { reasoning: "look it up", toolCalls: [{ toolName: "lookup_order", arguments: { order_id: "7" } }] },
        ),
        tools: [
          functionTool<{ order_id: string }>({
            name: "lookup_order",
            description: "Look up an order.",
            parameters: { type: "object", properties: { order_id: { type: "string" } }, required: ["order_id"] },
            effect: "read",
            execute: ({ order_id }) => ({ order_id, status: "shipped", weight: 1.5 }),
          }),
        ],
      },
      "StepwiseScriptedClientAgent",
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, deadlineFlow, limitedFlow, sessionPolicyFlow, builtInClashFlow, scriptedClientFlow],
      isWorkerInProcess: true,
    });
    sessionPolicySessions = new AgentSessionTestClient(environment.client, sessionPolicyFlow);
    builtInClashSessions = new AgentSessionTestClient(environment.client, builtInClashFlow);
    scriptedClientSessions = new AgentSessionTestClient(environment.client, scriptedClientFlow);
    sessions = new AgentSessionTestClient(environment.client, flow);
    deadlineSessions = new AgentSessionTestClient(environment.client, deadlineFlow);
    limitedSessions = new AgentSessionTestClient(environment.client, limitedFlow);
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  test("a read tool turn stores its transcript as it goes, and the next turn replays the history", async () => {
    const token = newToken();
    const sessionId = `stepwise-read-${token}`;
    await sessions.startSession(sessionId, `lookup gated-${token}`);
    const lookupGate = new FileGate(markerDirectory, "lookup");
    await lookupGate.waitUntilReached();

    const toolPhase = await sessions.getAgentSnapshot(sessionId);
    assert.equal(toolPhase.state?.status, "running_tools");
    assert.deepEqual(toolPhase.messages.map(describeMessage), [
      `1 turn-1 user lookup gated-${token}`,
      "2 turn-1 assistant model_output lookup_order",
    ]);
    assert.equal(JSON.parse(toolPhase.messages[1]?.toolArguments ?? "null").order_id, `gated-${token}`);
    lookupGate.release();
    await sessions.waitForCompletedTurns(sessionId, 1);

    const lookupResult = `{"order_id": "gated-${token}", "status": "shipped"}`;
    const firstTurn = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(firstTurn.messages.map(describeMessage), [
      `1 turn-1 user lookup gated-${token}`,
      "2 turn-1 assistant model_output lookup_order",
      `3 turn-1 tool tool_result lookup_order ${lookupResult}`,
      `4 turn-1 assistant lookup_order: ${lookupResult}`,
    ]);
    assert.equal(toolLog("lookup_order", sessionId).length, 1);
    const turnCompleted = (await sessions.listAgentEvents(sessionId)).find((event) => event.type === "turn_completed");
    assert.deepEqual(JSON.parse(turnCompleted?.payload ?? "null"), { message_count: 3 });

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const secondTurn = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      secondTurn.messages.at(-1)?.text,
      `history | user: lookup gated-${token} | assistant: lookup_order: ${lookupResult}`,
    );
    assert.deepEqual(secondTurn.usage, {
      modelCalls: 3,
      inputTokens: 36,
      outputTokens: 18,
      cachedInputTokens: 6,
      reasoningTokens: 3,
    });

    const modelCalls = readLoggedModelCalls(markerDirectory, token);
    assert.equal(modelCalls.length, 3, "one model call per RunModel");
    assert.deepEqual(modelCalls[0]?.toolNames, ["lookup_order", "refund_order", "charge_card", "ask_user", "durable_wait"]);
    assert.deepEqual(modelCalls[0]?.toolChoice, { type: "auto" });
    assert.deepEqual(modelCalls.map((call) => call.prompt[0]), [
      { role: "system", content: "You are the test agent. Turn turn-1, attempt 1." },
      { role: "system", content: "You are the test agent. Turn turn-1, attempt 1." },
      { role: "system", content: "You are the test agent. Turn turn-2, attempt 1." },
    ]);
    assert.deepEqual(modelCalls[1]?.prompt.map((message) => message.role), ["system", "user", "assistant", "tool"]);
    assert.deepEqual(
      modelCalls[2]?.prompt.map((message) => message.role),
      ["system", "user", "assistant", "tool", "assistant", "user"],
    );
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "CallTool",
      "ResumeTurn",
      "RunModel",
      "AwaitUserMessage",
      "RunModel",
    ]);
  });

  test("a write waits for approval, runs once in CallSingleAttemptTool, and reaches the model", async () => {
    const token = newToken();
    const sessionId = `stepwise-approval-${token}`;
    await sessions.startSession(sessionId, `refund ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "refund_order");
    assert.equal(interrupt.kind, "tool_approval");
    assert.equal(interrupt.interruptId, "call-1-0-refund_order");
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "yes"), false);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, `refund_order: refund issued for ${token}`);
    assert.deepEqual(toolLog("refund_order", sessionId), [
      `${sessionId} call-1-0-refund_order attempt 1 key ${sessionId}/turn-1/call-1-0-refund_order`,
    ]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("ask_user raises a question that answer_question resolves once, and the answer is the tool result", async () => {
    const token = newToken();
    const sessionId = `stepwise-question-${token}`;
    await sessions.startSession(sessionId, `ask ${token} choices red,blue strict`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "ask_user");
    assert.deepEqual(
      [interrupt.interruptId, interrupt.kind, interrupt.question, interrupt.choices, interrupt.allowsFreeText],
      ["call-1-0-ask_user", "question", `Which color for ${token}?`, ["red", "blue"], false],
    );
    const waiting = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waiting.toolCalls.map((toolCall) => [toolCall.toolName, toolCall.status, toolCall.effect, toolCall.requiresApproval]),
      [["ask_user", "awaiting_answer", "read", false]],
    );

    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), false);
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "green"), false);
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "   "), false);
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "blue"), true);
    assert.equal(await sessions.answerQuestion(sessionId, interrupt.interruptId, "red"), false);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.at(-1)?.text, 'answer: {"status":"answered","answer":"blue"}');
    assert.deepEqual(snapshot.pendingInterrupts, []);
    const modelCalls = readLoggedModelCalls(markerDirectory, token);
    assert.equal(modelCalls.length, 2);
    assert.deepEqual(modelCalls[1]?.prompt.at(-1), {
      role: "tool",
      content: [
        {
          type: "tool-result",
          toolCallId: "call-1-0-ask_user",
          toolName: "ask_user",
          output: { type: "text", value: '{"status":"answered","answer":"blue"}' },
        },
      ],
    });
    const events = await sessions.listAgentEvents(sessionId);
    assert.deepEqual(payloadOf(events, "interrupt_raised"), {
      interrupt_id: "call-1-0-ask_user",
      kind: "question",
      tool_call_id: "call-1-0-ask_user",
      tool_name: "ask_user",
      deadline: null,
      question: `Which color for ${token}?`,
      choices: ["red", "blue"],
      allows_free_text: false,
    });
    assert.deepEqual(payloadOf(events, "interrupt_resolved"), {
      interrupt_id: "call-1-0-ask_user",
      kind: "question",
      approved: true,
      answered: true,
      rejection_message: null,
      cancellation_reason: null,
    });
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitQuestionAnswer",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a declined question and a passed question deadline both reach the model as rejections", async () => {
    const declinedToken = newToken();
    const declinedSessionId = `stepwise-declined-${declinedToken}`;
    await sessions.startSession(declinedSessionId, `ask ${declinedToken}`);
    const declined = await sessions.waitForPendingInterrupt(declinedSessionId, "ask_user");
    assert.deepEqual([declined.choices, declined.allowsFreeText], [[], true]);
    assert.equal(await sessions.resolveInterrupt(declinedSessionId, declined.interruptId, false), true);
    await sessions.waitForCompletedTurns(declinedSessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(declinedSessionId)).messages.at(-1)?.text,
      "answer: The user declined to answer.",
    );

    const expiredToken = newToken();
    const expiredSessionId = `stepwise-expired-${expiredToken}`;
    await deadlineSessions.startSession(expiredSessionId, `ask ${expiredToken}`);
    const expired = await deadlineSessions.waitForPendingInterrupt(expiredSessionId, "ask_user");
    assert.equal(Date.parse(expired.deadline ?? "") - Date.parse(expired.createdAt), 3_000);
    await deadlineSessions.waitForCompletedTurns(expiredSessionId, 1);
    assert.equal(await deadlineSessions.answerQuestion(expiredSessionId, expired.interruptId, "blue"), false);
    assert.equal(
      (await deadlineSessions.getAgentSnapshot(expiredSessionId)).messages.at(-1)?.text,
      "answer: question deadline passed",
    );
  });

  test("durable_wait waits on a Timer in AwaitTimer, and an invalid duration fails without a Step", async () => {
    const token = newToken();
    const sessionId = `stepwise-wait-${token}`;
    const startedAt = Date.now();
    await sessions.startSession(sessionId, `wait 2 ${token}`);
    const waiting = await sessions.waitForToolCall(sessionId, "call-1-0-durable_wait", (toolCall) =>
      toolCall.status === "waiting_for_timer",
    );
    const waitUntil = Date.parse(waiting.waitUntil ?? "");
    assert.ok(waitUntil - startedAt >= 2_000 && waitUntil - startedAt < 10_000, `wait_until ${waiting.waitUntil}`);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.ok(Date.now() >= waitUntil, "the turn resumed before wait_until");

    const result = `{"status":"completed","waited_seconds":2,"reason":"cool down ${token}"}`;
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, `waited: ${result}`);
    assert.deepEqual(payloadOf(await sessions.listAgentEvents(sessionId), "timer_started"), {
      tool_call_id: "call-1-0-durable_wait",
      wait_until: waiting.waitUntil,
      reason: `cool down ${token}`,
    });
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitTimer",
      "ResumeTurn",
      "RunModel",
    ]);

    const invalidSessionId = `stepwise-wait-invalid-${newToken()}`;
    await sessions.startSession(invalidSessionId, "wait 0 invalid");
    await sessions.waitForCompletedTurns(invalidSessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(invalidSessionId)).messages.at(-1)?.text,
      "waited: The tool call failed: invalid durable_wait arguments: duration_seconds must be between 1 and 31536000",
    );
    assert.deepEqual(await executedStepTypes(invalidSessionId), ["StartAgentSession", "RunModel", "ResumeTurn", "RunModel"]);
  });

  test("reasoning and tool call parts are stored and replayed with their provider metadata unchanged", async () => {
    const token = newToken();
    const sessionId = `stepwise-reasoning-${token}`;
    await sessions.startSession(sessionId, `think ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    const storedOutput = JSON.parse(snapshot.messages[1]?.frameworkItem ?? "null") as {
      type: string;
      provider: string;
      api: string;
      item: string;
      text: string;
    };
    assert.deepEqual(
      [storedOutput.type, storedOutput.provider, storedOutput.api, storedOutput.text],
      ["model_output", "scripted.messages", "ai-sdk", "Checking the order."],
    );
    const expectedContent = [
      {
        type: "reasoning",
        text: `thinking about ${token}`,
        providerOptions: { scripted: { itemId: `rs-${token}`, signature: `sig-${token}`, encrypted: "e+/=" } },
      },
      { type: "text", text: "Checking the order." },
      {
        type: "tool-call",
        toolCallId: "call-1-0-lookup_order",
        toolName: "lookup_order",
        input: { order_id: token },
        providerOptions: { scripted: { itemId: `fc-${token}` } },
      },
    ];
    assert.equal(storedOutput.item, JSON.stringify({ role: "assistant", content: expectedContent }));
    const modelCalls = readLoggedModelCalls(markerDirectory, token);
    assert.equal(modelCalls.length, 2);
    assert.deepEqual(modelCalls[1]?.prompt[2], { role: "assistant", content: expectedContent });
    assert.equal(snapshot.messages.at(-1)?.text, `thought: {"order_id": "${token}", "status": "shipped"}`);
    const reasoningText = (await sessions.listAgentEvents(sessionId))
      .filter((event) => event.type === "reasoning_delta")
      .map((event) => (JSON.parse(event.payload) as { text: string }).text)
      .join("");
    assert.equal(reasoningText, `thinking about ${token}`);
  });

  test("a model of another provider gets the text and tool calls of a stored response without its reasoning", async () => {
    const otherModel = new MockLanguageModelV4({
      provider: "other.chat",
      modelId: "other-model",
      doStream: async () => ({
        stream: new ReadableStream({
          start(controller) {
            controller.enqueue({ type: "text-start", id: "a" });
            controller.enqueue({ type: "text-delta", id: "a", delta: "done" });
            controller.enqueue({ type: "text-end", id: "a" });
            controller.enqueue({
              type: "finish",
              usage: {
                inputTokens: { total: 1, noCache: 1, cacheRead: 0, cacheWrite: 0 },
                outputTokens: { total: 1, text: 1, reasoning: 0 },
              },
              finishReason: { unified: "stop", raw: "stop" },
            });
            controller.close();
          },
        }),
      }),
    });
    const response = await createAiSdkModelClient(otherModel).callModel(
      {
        instructions: "",
        inputItems: [
          { type: "user_text", text: "check" },
          {
            type: "model_output",
            provider: "scripted.messages",
            api: "ai-sdk",
            item: JSON.stringify({
              role: "assistant",
              content: [{ type: "reasoning", text: "secret", providerOptions: { scripted: { signature: "s" } } }],
            }),
            text: "Checking.",
            toolCalls: [{ toolCallId: "call-1", toolName: "lookup_order", arguments: '{"order_id":"7"}' }],
          },
          { type: "tool_result", toolCallId: "call-1", toolName: "lookup_order", output: "no" },
        ],
        tools: [],
        toolChoice: "none",
        settings: {},
        metadata: { flowId: "f", turnId: "turn-1", stepExecutionId: "RunModel-1", attempt: 1 },
        cancellationSignal: new AbortController().signal,
      },
      { writeTextDelta: () => undefined, writeReasoningDelta: () => undefined },
    );
    assert.equal(response.text, "done");
    assert.equal(otherModel.doStreamCalls.length, 1);
    assert.deepEqual(JSON.parse(JSON.stringify(otherModel.doStreamCalls[0]?.prompt)), [
      { role: "user", content: [{ type: "text", text: "check" }] },
      {
        role: "assistant",
        content: [
          { type: "text", text: "Checking." },
          { type: "tool-call", toolCallId: "call-1", toolName: "lookup_order", input: { order_id: "7" } },
        ],
      },
      {
        role: "tool",
        content: [
          {
            type: "tool-result",
            toolCallId: "call-1",
            toolName: "lookup_order",
            output: { type: "text", value: "no" },
          },
        ],
      },
    ]);
  });

  test("arguments that fail a function tool's schema reach the model as a tool failure", async () => {
    const token = newToken();
    const sessionId = `stepwise-schema-${token}`;
    await sessions.startSession(sessionId, `bad ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      "lookup_order: The tool call failed: invalid arguments for lookup_order: arguments.order_id must be string",
    );
    assert.deepEqual(toolLog("lookup_order", sessionId), []);
  });

  test("after maxModelCallsPerTurn model calls the next call offers no tools and the model answers", async () => {
    const token = newToken();
    const sessionId = `stepwise-limit-${token}`;
    await limitedSessions.startSession(sessionId, `loop ${token}`);
    await limitedSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await limitedSessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "no tools after 2 calls");
    const modelCalls: LoggedModelCall[] = readLoggedModelCalls(markerDirectory, token);
    assert.deepEqual(
      modelCalls.map((call) => call.toolNames.length),
      [5, 5, 0],
    );
    assert.deepEqual(
      (await executedStepTypes(sessionId)).filter((stepType) => stepType === "RunModel" || stepType === "CallTool"),
      ["RunModel", "CallTool", "RunModel", "CallTool", "RunModel"],
    );
  });

  test("a turn that fails after a tool ran stores the tool result before the turn-failure note", async () => {
    const token = newToken();
    const sessionId = `stepwise-failure-${token}`;
    await sessions.startSession(sessionId, `lookup ${token} then fail`);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /scripted model failure after the lookup/);
    const lookupResult = `{"order_id": "${token}", "status": "shipped"}`;
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user lookup ${token} then fail`,
      "2 turn-1 assistant model_output lookup_order",
      `3 turn-1 tool tool_result lookup_order ${lookupResult}`,
      "4 turn-1 user [Dex] The previous turn failed after these tool calls ran:\n" +
        `- lookup_order {"order_id":"${token}"} -> succeeded: ${lookupResult}\n` +
        "Do not repeat these tool calls unless the user asks again.",
    ]);
    assert.equal(toolLog("lookup_order", sessionId).length, 1);

    assert.equal(await sessions.sendUserMessage(sessionId, "history"), true);
    await sessions.waitForCompletedTurns(sessionId, 2);
    const modelCalls = readLoggedModelCalls(markerDirectory, token);
    assert.deepEqual(
      modelCalls.at(-1)?.prompt.map((message) => message.role),
      ["system", "user", "assistant", "tool", "user", "user"],
    );
  });

  test("a session tool policy that requires approval wins over a function tool's own requiresApproval", async () => {
    const token = newToken();
    const sessionId = `stepwise-session-policy-${token}`;
    // charge_card says requiresApproval false; the session's policy says true.
    await sessionPolicySessions.startSession(sessionId, `charge ${token}`);
    const approval = await sessionPolicySessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(approval.kind, "tool_approval");
    assert.equal(await sessionPolicySessions.resolveInterrupt(sessionId, approval.interruptId, false, "no charge"), true);
    await sessionPolicySessions.waitForCompletedTurns(sessionId, 1);
    assert.equal((await sessionPolicySessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "charge_card: no charge");
    assert.deepEqual(toolLog("charge_card", sessionId), []);
  });

  test("ask_user with null choices and allows_free_text treats them as absent", async () => {
    const token = newToken();
    const sessionId = `stepwise-ask-null-${token}`;
    await sessions.startSession(sessionId, `ask-null ${token}`);
    const question = await sessions.waitForPendingInterrupt(sessionId, "ask_user");
    assert.deepEqual([question.question, question.choices, question.allowsFreeText], [`Which color for ${token}?`, [], true]);
    assert.equal(await sessions.answerQuestion(sessionId, question.interruptId, "\u001c"), false);
    assert.equal(await sessions.answerQuestion(sessionId, question.interruptId, "teal"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      'answer: {"status":"answered","answer":"teal"}',
    );
  });

  test("a one-day durable_wait ends when its Timer is skipped", async () => {
    const token = newToken();
    const sessionId = `stepwise-skip-timer-${token}`;
    await sessions.startSession(sessionId, `wait 86400 ${token}`);
    const waiting = await sessions.waitForToolCall(sessionId, "call-1-0-durable_wait", (toolCall) =>
      toolCall.status === "waiting_for_timer",
    );
    assert.ok(Date.parse(waiting.waitUntil ?? "") - Date.now() > 86_000_000, `wait_until ${waiting.waitUntil}`);
    await environment.client.skipTimer(sessionId, StepExecutionId.of("AwaitTimer"), TimerId.byConditionIndex(0));
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(
      (await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text,
      `waited: {"status":"completed","waited_seconds":86400,"reason":"cool down ${token}"}`,
    );
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitTimer",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("a model that asks for tools after the model call limit ends the turn with not-run results", async () => {
    const token = newToken();
    const sessionId = `stepwise-stubborn-${token}`;
    await limitedSessions.startSession(sessionId, `stubborn ${token}`);
    const state = await limitedSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(state.lastTurnError, null);
    const lastMessage = (await limitedSessions.getAgentSnapshot(sessionId)).messages.at(-1);
    assert.deepEqual(
      [lastMessage?.role, lastMessage?.toolName, lastMessage?.toolOutput],
      ["tool", "lookup_order", "The tool call was not run: this turn reached its limit of 2 model calls."],
    );
    assert.deepEqual(readLoggedModelCalls(markerDirectory, token).map((call) => call.toolNames.length), [5, 5, 0]);
  });

  test("a function tool named like a built-in enabled only in the session fails before any model call", async () => {
    const token = newToken();
    const sessionId = `stepwise-built-in-clash-${token}`;
    await builtInClashSessions.startSession(sessionId, `echo ${token}`);
    const state = await builtInClashSessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.match(state.lastTurnError ?? "", /tool ask_user uses the name of an enabled built-in tool/);
    assert.deepEqual(readLoggedModelCalls(markerDirectory, token), []);
  });

  test("ScriptedModelClient drives a Stepwise turn without a model key", async () => {
    const sessionId = `stepwise-scripted-client-${newToken()}`;
    await scriptedClientSessions.startSession(sessionId, "where is order 7?");
    await scriptedClientSessions.waitForCompletedTurns(sessionId, 1);
    const snapshot = await scriptedClientSessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      snapshot.messages.map((message) => [message.role, message.text ?? message.toolOutput]),
      [
        ["user", "where is order 7?"],
        ["assistant", null],
        ["assistant", null],
        ["tool", '{"order_id": "7", "status": "shipped", "weight": 1.5}'],
        ["assistant", 'done: {"order_id": "7", "status": "shipped", "weight": 1.5}'],
      ],
    );
    const items = snapshot.messages.map((message) => JSON.parse(message.frameworkItem) as Record<string, unknown>);
    assert.deepEqual(items[1], {
      type: "model_output",
      provider: "dex-scripted",
      api: "scripted",
      item: '{"type":"reasoning","summary":"look it up"}',
      text: null,
      tool_calls: [],
    });
    assert.match(String(items[2]?.["item"]), /^\{"type":"function_call","call_id":"RunModel-1-0","name":"lookup_order"/);
  });

  test("function tool argument errors use the Python core's text", async () => {
    const runner = new StepwiseAgentRunner({
      name: "arguments",
      instructions: "",
      model: new ScriptedModelClient(() => ({})),
      tools: [
        functionTool({
          name: "pick",
          description: "Pick a value.",
          parameters: { type: "object", properties: { value: { enum: [1, "a"] } } },
          effect: "read",
          execute: ({ value }) => `picked ${String(value)}`,
        }),
      ],
    });
    const execute = (argumentsText: string) =>
      runner.executeDeferredToolCall({
        flowId: "f",
        toolCall: {
          toolCallId: "c",
          turnId: "turn-1",
          toolName: "pick",
          arguments: argumentsText,
          effect: "read",
          requiresApproval: false,
          status: "approved",
          output: null,
          error: null,
          rejectionMessage: null,
          deferralOrder: 0,
        },
        idempotencyKey: "f/turn-1/c",
        attempt: 1,
        events: { turnId: "turn-1", attempt: 1, writeEvent() {}, writeTextDelta() {}, writeReasoningDelta() {},
          writeToolCallStarted() {}, writeToolCallFinished() {} },
        cancellationSignal: new AbortController().signal,
        deadline: undefined,
        heartbeat: async () => undefined,
      });
    await assert.rejects(execute("{nope"), (error: unknown) =>
      error instanceof ToolExecutionError && error.message === "invalid arguments for pick: arguments are not valid JSON");
    await assert.rejects(execute('{"value": 2}'), (error: unknown) =>
      error instanceof ToolExecutionError &&
      error.message === 'invalid arguments for pick: arguments.value must be one of [1, "a"]');
    // Python's == makes true equal to 1, so true passes enum [1] in both cores.
    assert.deepEqual(await execute('{"value": true}'), { output: "picked true" });
    assert.deepEqual(await execute('{"value": 1.0}'), { output: "picked 1" });
  });

  test("an enabled built-in name is reserved, and a disabled one is an ordinary tool name", () => {
    const model = createAiSdkModelClient(new MockLanguageModelV4());
    const durableWaitTool = functionTool({
      name: "durable_wait",
      description: "An application tool that shares a built-in name.",
      parameters: { type: "object" },
      effect: "read",
      execute: () => "ok",
    });
    assert.throws(
      () =>
        createStepwiseAgentSessionFlow(
          { name: "clash", instructions: "", model, tools: [durableWaitTool], builtInTools: ["durable_wait"] },
          "ClashAgent",
        ),
      /uses the name of an enabled built-in tool/,
    );
    const flow = createStepwiseAgentSessionFlow(
      { name: "ordinary", instructions: "", model, tools: [durableWaitTool], builtInTools: ["ask_user"] },
      "OrdinaryAgent",
    );
    assert.deepEqual(flow.options.builtInTools, ["ask_user"]);
    assert.throws(
      () =>
        createAgentSessionFlow(new StepwiseAgentRunner({ name: "policy", instructions: "", model }), "PolicyAgent", {
          builtInTools: ["ask_user"],
          toolPolicies: { ask_user: { effect: "read" } },
        }),
      /names an enabled built-in tool/,
    );
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

function newToken(): string {
  return randomUUID().slice(0, 8);
}

function payloadOf(events: readonly AgentEvent[], type: string): unknown {
  const event = events.find((candidate) => candidate.type === type);
  return JSON.parse(event?.payload ?? "null");
}

function describeMessage(message: AgentMessage): string {
  const item = JSON.parse(message.frameworkItem) as { type: string };
  const detail =
    message.text ?? [item.type, message.toolName, message.toolOutput].filter((part) => part !== null).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}
