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

import type { Context } from "@earendil-works/pi-ai";
import { outcomeUnknownToolResult, type AgentMessage } from "@superdurable/dex-agents";
import {
  contextSummaryRequest,
  createStepwiseAgentSessionFlow,
  type ModelClient,
} from "@superdurable/dex-agents/stepwise";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  PrivateDexEngine,
  executeCompletedEvent,
  pollUntil,
  readStepHistory,
} from "@superdurable/dex-agents/testing";

import { createPiAiModelClient, piAgentFunctionTool, piToolProgressEventType } from "../../src/index.js";
import { FakeModelServer, createFakeOpenAiModel } from "../fixtures/fake-model-server.js";
import { createPiTestAgentFlow } from "../fixtures/pi-test-agent.js";

describe("Pi agents on the Stepwise session Flow with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let modelServer: FakeModelServer;
  let environment: AgentTestEnvironment;
  let sessions: AgentSessionTestClient;
  let preparedSessions: AgentSessionTestClient;
  let unknownItemSessions: AgentSessionTestClient;
  let compactingSessions: AgentSessionTestClient;
  let markerDirectory: string;
  let anthropicModelId = "claude-fake-1";
  let anthropicReasoningEffort: string | null | undefined;
  const prepareRequestFailures = new Set<string>();

  before(async () => {
    engine = await PrivateDexEngine.start();
    modelServer = await FakeModelServer.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createPiTestAgentFlow({
      flowType: "PiTestAgent",
      modelServerBaseUrl: modelServer.baseUrl,
      markerDirectory,
      anthropicModelId: () => anthropicModelId,
    });
    const preparedFlow = createPiTestAgentFlow({
      flowType: "PiPreparedRequestAgent",
      modelServerBaseUrl: modelServer.baseUrl,
      markerDirectory,
      prepareRequest: (context: Context, metadata) => {
        const token = findSessionToken(context);
        if (token !== undefined && prepareRequestFailures.delete(token)) {
          throw new Error("prepareRequest failed on the first attempt");
        }
        context.messages.push({
          role: "user",
          content: `[prepared for ${metadata.turnId} attempt ${metadata.attempt}]`,
          timestamp: 0,
        });
        return context;
      },
    });
    const compactingFlow = createPiTestAgentFlow({
      flowType: "PiCompactingAgent",
      modelServerBaseUrl: modelServer.baseUrl,
      markerDirectory,
      anthropicModelId: () => anthropicModelId,
      anthropicReasoningEffort: () => anthropicReasoningEffort,
      compaction: { contextWindowTokens: 20_000 },
    });
    const unknownItemFlow = createStepwiseAgentSessionFlow(
      {
        name: "pi-unknown-item",
        instructions: "",
        model: createPiAiModelClient(createFakeOpenAiModel(modelServer.baseUrl), { apiKey: "test-key" }),
        resolveModel: (settings) =>
          storedItemModelClients.get(String(settings["model"])) ??
          createPiAiModelClient(createFakeOpenAiModel(modelServer.baseUrl), { apiKey: "test-key" }),
      },
      "PiUnknownItemAgent",
    );
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow, preparedFlow, unknownItemFlow, compactingFlow],
      isWorkerInProcess: true,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    preparedSessions = new AgentSessionTestClient(environment.client, preparedFlow);
    unknownItemSessions = new AgentSessionTestClient(environment.client, unknownItemFlow);
    compactingSessions = new AgentSessionTestClient(environment.client, compactingFlow);
  });

  after(async () => {
    await environment?.close();
    await modelServer?.close();
    await engine?.stop();
  });

  test("two Pi tools of one response run in parallel tool Steps, and their results reach the next model call", async () => {
    const token = newToken();
    const sessionId = `pi-parallel-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? {
            text: "Checking. ",
            toolCalls: [
              { id: "call_lookup", name: "lookup_order", arguments: JSON.stringify({ order_id: `o-${token}` }) },
              { id: "call_weather", name: "get_weather", arguments: JSON.stringify({ city: `Paris-${token}` }) },
            ],
            usage: { input: 10, output: 5, cacheRead: 4 },
          }
        : { text: "Order shipped and it is sunny.", usage: { input: 20, output: 7, cacheRead: 0 } },
    );
    await sessions.startSession(sessionId, `check ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user check ${token}`,
      "2 turn-1 assistant Checking. ",
      `3 turn-1 tool tool_result lookup_order order o-${token} shipped`,
      `4 turn-1 tool tool_result get_weather sunny in Paris-${token}\n[image omitted: image/png]`,
      "5 turn-1 assistant Order shipped and it is sunny.",
    ]);
    const storedItem = JSON.parse(snapshot.messages[1]?.frameworkItem ?? "null") as {
      provider: string;
      api: string;
      item: string;
      tool_calls: unknown[];
    };
    assert.equal(storedItem.api, "pi-ai");
    assert.equal(storedItem.provider, "fake-openai");
    assert.equal(storedItem.tool_calls.length, 2);
    const envelope = JSON.parse(storedItem.item) as { format: number; piAiVersion: string; message: any };
    assert.equal(envelope.format, 1);
    assert.equal(envelope.piAiVersion, "1.5.1");
    assert.equal(envelope.message.stopReason, "toolUse");
    assert.deepEqual(
      [envelope.message.role, envelope.message.api, envelope.message.provider, envelope.message.model],
      ["assistant", "openai-completions", "fake-openai", "fake-gpt"],
    );
    for (const localField of ["timestamp", "durationMs", "diagnostics"]) {
      assert.equal(localField in envelope.message, false, `${localField} is not stored`);
    }
    assert.deepEqual(envelope.message.usage, { input: 10, output: 5, cacheRead: 4, cacheWrite: 0, reasoning: 0, totalTokens: 19 });
    assert.deepEqual(snapshot.usage, {
      modelCalls: 2,
      inputTokens: 34,
      outputTokens: 12,
      cachedInputTokens: 4,
      reasoningTokens: 0,
    });
    assert.deepEqual(await describeToolCalls(sessionId, ["call_lookup", "call_weather"]), [
      ["lookup_order", "idempotent_write", false, "succeeded"],
      ["get_weather", "read", false, "succeeded"],
    ]);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2);
    assert.deepEqual(
      requests[0]?.body["tools"].map((tool: { function: { name: string } }) => tool.function.name).slice(0, 4),
      ["lookup_order", "get_weather", "fail_lookup", "send_email"],
    );
    assert.deepEqual(
      requests[1]?.body["messages"].map((message: { role: string }) => message.role),
      ["system", "user", "assistant", "tool", "tool"],
    );
    assert.deepEqual(
      requests[1]?.body["messages"].slice(3).map((message: { content: string }) => message.content),
      [`order o-${token} shipped`, `sunny in Paris-${token}\n[image omitted: image/png]`],
    );
    const progress = (await sessions.listAgentEvents(sessionId)).filter((event) => event.type === piToolProgressEventType);
    assert.deepEqual(progress.map((event) => JSON.parse(event.payload)), [
      { tool_call_id: "call_lookup", tool_name: "lookup_order", text: `looking up o-${token}` },
    ]);
    const stepTypes = await executedStepTypes(sessionId);
    assert.deepEqual(stepTypes.slice(0, 2), ["StartAgentSession", "RunModel"]);
    assert.deepEqual(stepTypes.slice(2, 4).sort(), ["CallTool", "CallTool"]);
    assert.deepEqual(stepTypes.slice(4), ["ResumeTurn", "RunModel"]);
  });

  test("Anthropic thinking signatures replay byte for byte within a turn and in the next turn", async () => {
    const token = newToken();
    const sessionId = `pi-thinking-${token}`;
    const signature = `sig-${token}-${"x".repeat(40)}+/=`;
    modelServer.script(token, (_request, index) => {
      switch (index) {
        case 0:
          return {
            thinking: { text: "I should look it up.", signature },
            toolCalls: [{ id: "toolu_1", name: "lookup_order", arguments: JSON.stringify({ order_id: `o-${token}` }) }],
          };
        case 1:
          return { text: "It shipped." };
        default:
          return { thinking: { text: "Second turn.", signature: `${signature}-2` }, text: "Still shipped." };
      }
    });
    await sessions.startSession(sessionId, `thinking ${token}`, {}, { model: "anthropic" });
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.sendUserMessage(sessionId, `again ${token}`), true);
    await sessions.waitForCompletedTurns(sessionId, 2);

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 3);
    assert.equal(requests[0]?.body["thinking"]?.type, "enabled");
    const expectedAssistant = [
      { type: "thinking", thinking: "I should look it up.", signature },
      { type: "tool_use", id: "toolu_1", name: "lookup_order", input: { order_id: `o-${token}` } },
    ];
    for (const request of requests.slice(1)) {
      const assistant = request.body["messages"].find((message: { role: string }) => message.role === "assistant");
      assert.deepEqual(stripCacheControl(assistant.content), expectedAssistant);
    }
    const lastMessages = requests[2]?.body["messages"] as { role: string; content: unknown[] }[];
    assert.deepEqual(stripCacheControl(lastMessages[3]?.content), [{ type: "text", text: "It shipped." }]);
    const reasoning = (await sessions.listAgentEvents(sessionId)).filter((event) => event.type === "reasoning_delta");
    assert.deepEqual(reasoning.map((event) => JSON.parse(event.payload).text), ["I should look it up.", "Second turn."]);
  });

  test("a session that switches from Anthropic to OpenAI replays the Anthropic messages without their signatures", async () => {
    const token = newToken();
    const sessionId = `pi-switch-${token}`;
    modelServer.script(token, (request) =>
      request.format === "anthropic"
        ? { thinking: { text: "Thinking first.", signature: `sig-${token}` }, text: "Anthropic answer." }
        : { text: "OpenAI answer." },
    );
    await sessions.startSession(sessionId, `switch ${token}`, {}, { model: "anthropic" });
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await sessions.updateSessionSettings(sessionId, { model: "openai" }), true);
    assert.equal(await sessions.sendUserMessage(sessionId, `now openai ${token}`), true);
    await sessions.waitForCompletedTurns(sessionId, 2);

    const requests = modelServer.requests(token);
    assert.deepEqual(requests.map((request) => request.format), ["anthropic", "openai"]);
    const openAiMessages = requests[1]?.body["messages"] as { role: string; content: unknown }[];
    assert.deepEqual(openAiMessages.map((message) => message.role), ["system", "user", "assistant", "user"]);
    assert.equal(openAiMessages[2]?.content, "Anthropic answer.");
    assert.equal(JSON.stringify(requests[1]?.body).includes(`sig-${token}`), false);
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "OpenAI answer.");
  });

  test("a Pi tool that throws reaches the model as a failed result and the session continues", async () => {
    const token = newToken();
    const sessionId = `pi-tool-error-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0 ? { toolCalls: [{ id: "call_fail", name: "fail_lookup", arguments: "{}" }] } : { text: "The warehouse is down." },
    );
    await sessions.startSession(sessionId, `fail ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(await describeToolCalls(sessionId, ["call_fail"]), [["fail_lookup", "read", false, "failed"]]);
    assert.equal(snapshot.messages.at(-1)?.text, "The warehouse is down.");
    const toolMessage = modelServer.requests(token)[1]?.body["messages"].at(-1) as { role: string; content: string };
    assert.equal(toolMessage.role, "tool");
    assert.match(toolMessage.content, /warehouse offline/);
  });

  test("an HTTP 500 and a stream broken after text retry RunModel and store no partial reply", async () => {
    const token = newToken();
    const sessionId = `pi-model-retry-${token}`;
    modelServer.script(token, (_request, index) => {
      if (index === 0) {
        return { isServerError: true };
      }
      if (index === 1) {
        return { text: "partial answer", isCutBeforeStop: true };
      }
      return { text: "complete answer" };
    });
    await sessions.startSession(sessionId, `retry ${token}`);
    const state = await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
    assert.equal(state.lastTurnError, null);

    assert.equal(modelServer.requests(token).length, 3);
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(snapshot.messages.map(describeMessage), [
      `1 turn-1 user retry ${token}`,
      "2 turn-1 assistant complete answer",
    ]);
    assert.equal(snapshot.usage.modelCalls, 1);
    const runModelAttempts = (await readStepHistory(engine.serverAddress, sessionId)).filter(
      (entry) => entry.stepType === "RunModel" && entry.eventType === executeCompletedEvent,
    );
    assert.equal(runModelAttempts.length, 1);
  });

  test("a stream broken after a complete tool call retries RunModel and never runs the tool", async () => {
    const token = newToken();
    const sessionId = `pi-cut-tool-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? {
            toolCalls: [{ id: "call_email", name: "send_email", arguments: JSON.stringify({ to: `cut-${token}` }) }],
            isCutBeforeStop: true,
          }
        : { text: "I did not send anything." },
    );
    await sessions.startSession(sessionId, `cut ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(await describeToolCalls(sessionId, ["call_email"]), [[undefined, undefined, undefined, undefined]]);
    assert.deepEqual(snapshot.pendingInterrupts, []);
    assert.equal(snapshot.messages.at(-1)?.text, "I did not send anything.");
    assert.equal(new InvocationLog(join(markerDirectory, "send_email.log")).names().some((name) => name.includes(token)), false);
    assert.deepEqual(await executedStepTypes(sessionId), ["StartAgentSession", "RunModel"]);
  });

  test("cancelling the turn aborts the streaming provider request", async () => {
    const token = newToken();
    const sessionId = `pi-cancel-${token}`;
    let releaseStream: () => void = () => undefined;
    const streamHeld = new Promise<void>((resolve) => {
      releaseStream = resolve;
    });
    modelServer.script(token, () => ({ text: "thinking out loud", pauseAfterText: () => streamHeld }));
    try {
      await sessions.startSession(sessionId, `cancel ${token}`);
      await pollUntil(
        async () =>
          (await sessions.listAgentEvents(sessionId)).some((event) => event.type === "text_delta") ? true : undefined,
        { description: `text_delta in ${sessionId}` },
      );
      assert.equal(await sessions.cancelTurn(sessionId), true);
      await pollUntil(() => (modelServer.requests(token)[0]?.isAbortedByClient === true ? true : undefined), {
        description: "the provider request is aborted",
      });
      await sessions.waitForAgentState(
        sessionId,
        (state) => state.status === "waiting_for_user_message",
        "the cancelled turn to end",
      );
    } finally {
      releaseStream();
    }
    const events = await sessions.listAgentEvents(sessionId);
    assert.equal(events.some((event) => event.type === "turn_cancelled"), true);
  });

  test("a non_idempotent_write Pi tool that throws records outcome_unknown, and one that reports isError fails", async () => {
    const token = newToken();
    const sessionId = `pi-write-error-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? {
            toolCalls: [
              { id: "call_throw", name: "submit_payment", arguments: JSON.stringify({ outcome: `throw-${token}` }) },
              { id: "call_reject", name: "submit_payment", arguments: JSON.stringify({ outcome: `reject-${token}` }) },
            ],
          }
        : { text: "One payment is unknown and one was declined." },
    );
    await sessions.startSession(sessionId, `pay ${token}`);
    for (let approved = 0; approved < 2; approved++) {
      const interrupt = await sessions.waitForPendingInterrupt(sessionId, "submit_payment");
      assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    }
    await sessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    assert.deepEqual(await describeToolCalls(sessionId, ["call_throw", "call_reject"]), [
      ["submit_payment", "non_idempotent_write", true, "outcome_unknown"],
      ["submit_payment", "non_idempotent_write", true, "failed"],
    ]);
    const outputs = new Map(
      (await sessions.getAgentSnapshot(sessionId)).messages
        .filter((message) => message.role === "tool")
        .map((message) => [message.toolCallId, message.toolOutput]),
    );
    assert.equal(outputs.get("call_throw"), outcomeUnknownToolResult);
    assert.equal(outputs.get("call_reject"), "The tool call failed: card declined");
    const runs = new InvocationLog(join(markerDirectory, "submit_payment.log")).names().filter((name) => name.includes(token));
    assert.deepEqual(runs.sort(), [`reject-${token} call_reject`, `throw-${token} call_throw`]);
  });

  test("an unclassified Pi tool waits for resolve_interrupt before it runs", async () => {
    const token = newToken();
    const sessionId = `pi-approval-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_delete", name: "delete_file", arguments: JSON.stringify({ path: `f-${token}` }) }] }
        : { text: "Deleted." },
    );
    await sessions.startSession(sessionId, `delete ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "delete_file");
    assert.equal(interrupt.kind, "tool_approval");
    const deleteLog = new InvocationLog(join(markerDirectory, "delete_file.log"));
    assert.equal(deleteLog.names().some((name) => name.includes(token)), false);
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    assert.deepEqual(deleteLog.names().filter((name) => name.includes(token)), [`f-${token} call_delete`]);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
    ]);
  });

  test("prepareArguments repairs arguments before pi-ai validates them", async () => {
    const token = newToken();
    const sessionId = `pi-prepare-arguments-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_rename", name: "rename_customer", arguments: JSON.stringify({ old_name: `n-${token}` }) }] }
        : { text: "Renamed." },
    );
    await sessions.startSession(sessionId, `rename ${token}`);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const toolMessage = (await sessions.getAgentSnapshot(sessionId)).messages.find((message) => message.role === "tool");
    assert.equal(toolMessage?.toolOutput, `renamed to n-${token}`);
  });

  test("a tool loop whose model changed mid-turn sends no thinking for the rest of it, and the next turn thinks", async () => {
    const token = newToken();
    const sessionId = `pi-model-change-${token}`;
    const gateName = `gated-${token}`;
    modelServer.script(token, (_request, index) => {
      switch (index) {
        case 0:
          return {
            thinking: { text: "Look it up.", signature: `sig-${token}` },
            toolCalls: [{ id: "toolu_gated", name: "lookup_order", arguments: JSON.stringify({ order_id: gateName }) }],
          };
        case 1:
          return {
            toolCalls: [{ id: "toolu_again", name: "lookup_order", arguments: JSON.stringify({ order_id: `again-${token}` }) }],
          };
        default:
          return { text: `answer ${index}` };
      }
    });
    try {
      await sessions.startSession(sessionId, `change ${token}`, {}, { model: "anthropic" });
      const gate = new FileGate(markerDirectory, gateName);
      await gate.waitUntilReached();
      anthropicModelId = "claude-fake-2";
      gate.release();
      await sessions.waitForCompletedTurns(sessionId, 1);
      assert.equal(await sessions.sendUserMessage(sessionId, `next ${token}`), true);
      await sessions.waitForCompletedTurns(sessionId, 2);
    } finally {
      anthropicModelId = "claude-fake-1";
    }

    const requests = modelServer.requests(token);
    assert.deepEqual(
      requests.map((request) => [request.body["model"], request.body["thinking"]?.type === "enabled"]),
      [
        ["claude-fake-1", true],
        ["claude-fake-2", false],
        ["claude-fake-2", false],
        ["claude-fake-2", true],
      ],
    );
  });

  test("thinking stays off in a tool loop after compaction removed the other model's output", async () => {
    const token = newToken();
    const sessionId = `pi-compacted-change-${token}`;
    const gateName = `gated-huge-${token}`;
    let agentCallCount = 0;
    modelServer.script(token, (request) => {
      if (JSON.stringify(request.body).includes(contextSummaryRequest)) {
        return { text: `summary of the earlier work ${token}` };
      }
      agentCallCount += 1;
      switch (agentCallCount) {
        case 1:
          return {
            thinking: { text: "Look it up.", signature: `sig-${token}` },
            toolCalls: [{ id: "toolu_huge", name: "lookup_order", arguments: JSON.stringify({ order_id: gateName }) }],
          };
        case 2:
          return {
            toolCalls: [{ id: "toolu_small", name: "lookup_order", arguments: JSON.stringify({ order_id: `small-${token}` }) }],
          };
        default:
          return { text: `answer ${agentCallCount}` };
      }
    });
    try {
      await compactingSessions.startSession(sessionId, `compact ${token}`, {}, { model: "anthropic" });
      const gate = new FileGate(markerDirectory, gateName);
      await gate.waitUntilReached();
      anthropicModelId = "claude-fake-2";
      gate.release();
      await compactingSessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
      assert.equal(await compactingSessions.sendUserMessage(sessionId, `next ${token}`), true);
      await compactingSessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    } finally {
      anthropicModelId = "claude-fake-1";
    }

    const requests = modelServer.requests(token);
    const agentRequests = requests.filter((request) => !JSON.stringify(request.body).includes(contextSummaryRequest));
    assert.ok(requests.length > agentRequests.length, "the session compacted");
    assert.deepEqual(
      agentRequests.map((request) => [request.body["model"], request.body["thinking"]?.type === "enabled"]),
      [
        ["claude-fake-1", true],
        ["claude-fake-2", false],
        ["claude-fake-2", false],
        ["claude-fake-2", true],
      ],
    );
    const thirdRequest = JSON.stringify(agentRequests[2]?.body);
    assert.equal(thirdRequest.includes("toolu_huge"), false, "compaction removed the first model's output");
    assert.equal(thirdRequest.includes("toolu_small"), true);
    const storedFlags = (await compactingSessions.getAgentSnapshot(sessionId)).messages
      .filter((message) => message.role === "assistant")
      .map((message) => {
        const item = JSON.parse(message.frameworkItem) as { item: string };
        return (JSON.parse(item.item) as { thinkingDisabledInToolLoop?: boolean }).thinkingDisabledInToolLoop ?? false;
      });
    assert.deepEqual(storedFlags, [false, true, true, false]);
  });

  test("calls without reasoningEffort keep the tool-loop flag, so restored thinking stays off after compaction", async () => {
    const token = newToken();
    const sessionId = `pi-unset-effort-${token}`;
    const switchGate = `gated-huge-1-${token}`;
    const restoreGate = `gated-3-${token}`;
    let agentCallCount = 0;
    modelServer.script(token, (request) => {
      if (JSON.stringify(request.body).includes(contextSummaryRequest)) {
        return { text: `summary ${token}` };
      }
      agentCallCount += 1;
      const lookup = (id: string, orderId: string) => ({
        toolCalls: [{ id, name: "lookup_order", arguments: JSON.stringify({ order_id: orderId }) }],
      });
      switch (agentCallCount) {
        case 1:
          return { thinking: { text: "Look it up.", signature: `sig-${token}` }, ...lookup("toolu_1", switchGate) };
        case 2:
          return lookup("toolu_2", `huge-2-${token}`);
        case 3:
          return lookup("toolu_3", restoreGate);
        default:
          return { text: `answer ${agentCallCount}` };
      }
    });
    try {
      await compactingSessions.startSession(sessionId, `unset ${token}`, {}, { model: "anthropic" });
      await new FileGate(markerDirectory, switchGate).waitUntilReached();
      anthropicModelId = "claude-fake-2";
      anthropicReasoningEffort = null;
      new FileGate(markerDirectory, switchGate).release();
      await new FileGate(markerDirectory, restoreGate).waitUntilReached();
      anthropicReasoningEffort = undefined;
      new FileGate(markerDirectory, restoreGate).release();
      await compactingSessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });
      assert.equal(await compactingSessions.sendUserMessage(sessionId, `next ${token}`), true);
      await compactingSessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
    } finally {
      anthropicModelId = "claude-fake-1";
      anthropicReasoningEffort = undefined;
    }

    const agentRequests = modelServer
      .requests(token)
      .filter((request) => !JSON.stringify(request.body).includes(contextSummaryRequest));
    assert.deepEqual(
      agentRequests.map((request) => [request.body["model"], request.body["thinking"]?.type === "enabled"]),
      [
        ["claude-fake-1", true],
        ["claude-fake-2", false],
        ["claude-fake-2", false],
        ["claude-fake-2", false],
        ["claude-fake-2", true],
      ],
    );
    const fourthRequest = JSON.stringify(agentRequests[3]?.body);
    assert.equal(fourthRequest.includes("toolu_1"), false, "compaction removed the first model's output");
    assert.equal(fourthRequest.includes("toolu_2"), false, "compaction removed the first flagged output");
    assert.equal(fourthRequest.includes("toolu_3"), true);
    const storedFlags = (await compactingSessions.getAgentSnapshot(sessionId)).messages
      .filter((message) => message.role === "assistant")
      .map((message) => {
        const item = JSON.parse(message.frameworkItem) as { item: string };
        return (JSON.parse(item.item) as { thinkingDisabledInToolLoop?: boolean }).thinkingDisabledInToolLoop ?? false;
      });
    assert.deepEqual(storedFlags, [false, true, true, true, false]);
  });

  test("an AgentTool's replay gives its effect, and a tool with neither replay nor effect fails at definition", async () => {
    const token = newToken();
    const sessionId = `pi-effects-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_email", name: "send_email", arguments: JSON.stringify({ to: `e-${token}` }) }] }
        : { text: "Sent." },
    );
    await sessions.startSession(sessionId, `email ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    const waiting = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      waiting.toolCalls.map((toolCall) => [toolCall.toolName, toolCall.effect, toolCall.requiresApproval]),
      [["send_email", "non_idempotent_write", true]],
    );
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    assert.deepEqual(await executedStepTypes(sessionId), [
      "StartAgentSession",
      "RunModel",
      "AwaitToolApproval",
      "CallSingleAttemptTool",
      "ResumeTurn",
      "RunModel",
    ]);

    const undeclared = {
      name: "undeclared",
      description: "A tool without replay.",
      parameters: { type: "object" },
      execute: async () => ({ content: [], details: undefined }),
    };
    assert.throws(() => piAgentFunctionTool(undeclared), {
      name: "TypeError",
      message: /declares no replay, so pass its effect/,
    });
    assert.equal(piAgentFunctionTool(undeclared, { effect: "read" }).effect, "read");
    assert.equal(piAgentFunctionTool({ ...undeclared, replay: "safe" }, { effect: "read" }).effect, "read");
    assert.throws(() => piAgentFunctionTool({ ...undeclared, replay: "safe", executionMode: "sequential" }), {
      name: "TypeError",
      message: /sequential/,
    });
  });

  test("prepareRequest changes only the request it prepares, and a failure retries RunModel", async () => {
    const token = newToken();
    const sessionId = `pi-prepare-request-${token}`;
    prepareRequestFailures.add(token);
    modelServer.script(token, (_request, index) =>
      index === 0
        ? { toolCalls: [{ id: "call_lookup", name: "lookup_order", arguments: JSON.stringify({ order_id: `p-${token}` }) }] }
        : { text: "Prepared answer." },
    );
    await preparedSessions.startSession(sessionId, `prepare ${token}`);
    await preparedSessions.waitForCompletedTurns(sessionId, 1, { timeoutMs: 120_000 });

    const requests = modelServer.requests(token);
    assert.equal(requests.length, 2, "the failed attempt sent no request");
    assert.deepEqual(
      requests.map((request) =>
        (request.body["messages"] as { role: string; content: unknown }[])
          .filter((message) => typeof message.content === "string" && message.content.startsWith("[prepared"))
          .map((message) => message.content),
      ),
      [["[prepared for turn-1 attempt 2]"], ["[prepared for turn-1 attempt 1]"]],
    );
    assert.equal(requests[1]?.body["messages"].at(-1).content, "[prepared for turn-1 attempt 1]");
    const snapshot = await preparedSessions.getAgentSnapshot(sessionId);
    assert.equal(snapshot.messages.some((message) => (message.text ?? "").includes("[prepared")), false);
    assert.equal(snapshot.messages.at(-1)?.text, "Prepared answer.");
  });

  test("pi-durable tools: replay gives the effect, and api use and control fail visibly", async () => {
    const token = newToken();
    const sessionId = `pi-durable-${token}`;
    modelServer.script(token, (_request, index) =>
      index === 0
        ? {
            toolCalls: [
              { id: "call_forecast", name: "durable_forecast", arguments: JSON.stringify({ city: `Oslo-${token}` }) },
              { id: "call_nested", name: "durable_nested_call", arguments: "{}" },
              { id: "call_finish", name: "durable_finish_turn", arguments: "{}" },
              { id: "call_charge", name: "durable_charge", arguments: JSON.stringify({ amount: `5-${token}` }) },
            ],
          }
        : { text: "Mixed results." },
    );
    await sessions.startSession(sessionId, `durable ${token}`);
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "durable_charge");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    await sessions.waitForCompletedTurns(sessionId, 1);

    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.deepEqual(
      await describeToolCalls(sessionId, ["call_forecast", "call_nested", "call_finish", "call_charge"]),
      [
        ["durable_forecast", "idempotent_write", false, "succeeded"],
        ["durable_nested_call", "read", false, "failed"],
        ["durable_finish_turn", "read", false, "failed"],
        ["durable_charge", "non_idempotent_write", true, "succeeded"],
      ],
    );
    const outputs = new Map(
      snapshot.messages.filter((message) => message.role === "tool").map((message) => [message.toolName, message.toolOutput]),
    );
    assert.equal(outputs.get("durable_forecast"), `forecast for Oslo-${token}: rain`);
    assert.match(outputs.get("durable_nested_call") ?? "", /used api\.executeTool, which is not supported on Dex/);
    assert.match(outputs.get("durable_finish_turn") ?? "", /returned control \{"terminate":true\}, which is not supported on Dex/);
    assert.equal(outputs.get("durable_charge"), `charged 5-${token}`);
  });

  test("a stored pi-ai item that replay cannot read fails the turn naming both versions and the field", async () => {
    const cases: readonly [string, RegExp][] = [
      ["future", /from pi-ai 9\.0\.0 cannot be replayed by pi-ai 1\.5\.1: its format is 2, not 1/],
      [
        "mistyped-signature",
        /from pi-ai 1\.6\.0 cannot be replayed by pi-ai 1\.5\.1: message\.content\[0\]\.thinkingSignature is not a string/,
      ],
      ["missing-tool-call-name", /from pi-ai 1\.5\.1 cannot be replayed by pi-ai 1\.5\.1: message\.content\[0\]\.name is missing/],
    ];
    for (const [storedModel, expectedError] of cases) {
      const token = newToken();
      const sessionId = `pi-unknown-item-${storedModel}-${token}`;
      modelServer.script(token, () => ({ text: "never sent" }));
      await unknownItemSessions.startSession(sessionId, `stored ${token}`, {}, { model: storedModel });
      await unknownItemSessions.waitForCompletedTurns(sessionId, 1);
      assert.equal(await unknownItemSessions.updateSessionSettings(sessionId, { model: "openai" }), true);
      assert.equal(await unknownItemSessions.sendUserMessage(sessionId, `now ${token}`), true);
      const state = await unknownItemSessions.waitForCompletedTurns(sessionId, 2, { timeoutMs: 120_000 });
      assert.match(state.lastTurnError ?? "", expectedError);
      assert.equal(modelServer.requests(token).length, 0);
    }
  });

  test("a stored pi-ai item from another pi-ai version with extra fields replays", async () => {
    const token = newToken();
    const sessionId = `pi-older-item-${token}`;
    modelServer.script(token, () => ({ text: "replayed fine" }));
    await unknownItemSessions.startSession(sessionId, `older ${token}`, {}, { model: "older-with-extras" });
    await unknownItemSessions.waitForCompletedTurns(sessionId, 1);
    assert.equal(await unknownItemSessions.updateSessionSettings(sessionId, { model: "openai" }), true);
    assert.equal(await unknownItemSessions.sendUserMessage(sessionId, `now ${token}`), true);
    const state = await unknownItemSessions.waitForCompletedTurns(sessionId, 2);

    assert.equal(state.lastTurnError, null);
    const messages = modelServer.requests(token)[0]?.body["messages"] as { role: string; content: unknown }[];
    assert.deepEqual(messages.map((message) => message.role), ["user", "assistant", "user"]);
    assert.equal(messages[1]?.content, "answer from an older pi-ai");
    assert.equal((await unknownItemSessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "replayed fine");
  });

  /** Each call's name, effect, and approval from `tool_call_deferred`, and its final status from `tool_call_finished`. */
  async function describeToolCalls(sessionId: string, toolCallIds: readonly string[]): Promise<unknown[]> {
    const payloads = (await sessions.listAgentEvents(sessionId)).map((event) => ({
      type: event.type,
      payload: JSON.parse(event.payload) as Record<string, unknown>,
    }));
    const findPayload = (type: string, toolCallId: string) =>
      payloads.find((event) => event.type === type && event.payload["tool_call_id"] === toolCallId)?.payload;
    return toolCallIds.map((toolCallId) => {
      const deferred = findPayload("tool_call_deferred", toolCallId);
      const finished = findPayload("tool_call_finished", toolCallId);
      return [deferred?.["tool_name"], deferred?.["effect"], deferred?.["requires_approval"], finished?.["status"]];
    });
  }

  async function executedStepTypes(sessionId: string): Promise<string[]> {
    return (await readStepHistory(engine.serverAddress, sessionId))
      .filter((entry) => entry.eventType === executeCompletedEvent)
      .map((entry) => entry.stepType);
  }
});

/** A model client that answers with one fixed stored pi-ai item, as another version of this package might. */
function createStoredItemModelClient(stored: Record<string, unknown>): ModelClient {
  return {
    async callModel() {
      return {
        outputItems: [
          { type: "model_output", provider: "fake-openai", api: "pi-ai", item: JSON.stringify(stored), text: "stored", toolCalls: [] },
        ],
        text: "stored",
        toolCalls: [],
        usage: { modelCalls: 1, inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, reasoningTokens: 0 },
        responseModel: "stored",
        finishReason: "stop",
      };
    },
    estimateInputTokens: () => 0,
  };
}

function storedMessage(content: unknown[], extraFields: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    role: "assistant",
    content,
    api: "openai-completions",
    provider: "fake-openai",
    model: "fake-gpt",
    stopReason: "stop",
    usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2 },
    ...extraFields,
  };
}

const storedItemModelClients = new Map<string, ModelClient>([
  ["future", createStoredItemModelClient({ format: 2, piAiVersion: "9.0.0", message: storedMessage([]) })],
  [
    "older-with-extras",
    createStoredItemModelClient({
      format: 1,
      piAiVersion: "1.4.0",
      futureEnvelopeField: true,
      message: storedMessage(
        [
          { type: "thinking", thinking: "old thoughts", thinkingSignature: "reasoning_content", futurePartField: 1 },
          { type: "text", text: "answer from an older pi-ai", futurePartField: "x" },
        ],
        { endTurn: true, futureMessageField: { nested: true } },
      ),
    }),
  ],
  [
    "mistyped-signature",
    createStoredItemModelClient({
      format: 1,
      piAiVersion: "1.6.0",
      message: storedMessage([{ type: "thinking", thinking: "t", thinkingSignature: 5 }]),
    }),
  ],
  [
    "missing-tool-call-name",
    createStoredItemModelClient({
      format: 1,
      piAiVersion: "1.5.1",
      message: storedMessage([{ type: "toolCall", id: "call_1", arguments: {} }]),
    }),
  ],
]);

function newToken(): string {
  return randomUUID().slice(0, 8);
}

function findSessionToken(context: Context): string | undefined {
  const first = context.messages.find((message) => message.role === "user");
  const text = typeof first?.content === "string" ? first.content : "";
  return text.split(" ")[1];
}

function describeMessage(message: AgentMessage): string {
  const item = JSON.parse(message.frameworkItem) as { type: string };
  const detail =
    message.text ?? [item.type, message.toolName, message.toolOutput].filter((part) => part !== null).join(" ");
  return `${message.sequence} ${message.turnId} ${message.role} ${detail}`;
}

/** Anthropic content blocks without pi-ai's prompt cache markers, which move to the newest message. */
function stripCacheControl(content: unknown): unknown {
  return (content as Record<string, unknown>[]).map(({ cache_control: _cacheControl, ...block }) => block);
}
