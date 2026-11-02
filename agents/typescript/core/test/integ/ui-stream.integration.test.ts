// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { after, afterEach, before, describe, test } from "node:test";

import { Worker, openBlobCache, type BlobCache } from "@superdurable/dex";
import {
  DefaultChatTransport,
  lastAssistantMessageIsCompleteWithApprovalResponses,
  readUIMessageStream,
  validateUIMessages,
  type UIMessage,
  type UIMessageChunk,
} from "ai";

import type { AgentFrameworkRunner, AgentSnapshot, DeferredToolOutcome } from "../../src/index.js";
import type { AgentSessionFlow } from "../../src/index.js";
import {
  AgentSessionTestClient,
  AgentTestEnvironment,
  FileGate,
  InvocationLog,
  KillableWorkerProcess,
  PrivateDexEngine,
  pollUntil,
} from "../../src/testing/index.js";
import {
  agentResumeTokenHeaderName,
  convertAgentEventToJson,
  convertAgentSnapshotToJson,
  maximumAgentUIStreamResumeTokenLength,
  readAgentToolApprovalResponses,
  resolveAgentToolApprovalsWithUIMessageStream,
  streamAgentUIMessageChunks,
  type AgentSessionUIMessages,
  type AgentUIMessage,
  type AgentUIMessageChunk,
} from "../../src/ui-stream/index.js";
import {
  createAgentUIStreamCursor,
  decodeAgentUIStreamCursor,
  encodeAgentUIStreamCursor,
} from "../../src/ui-stream/agent-ui-stream-cursor.js";
import {
  AgentUIStreamTestChat,
  AgentUIStreamTestServer,
  RecordingFetch,
  describeUIMessageParts,
  type ResponseCut,
} from "../fixtures/agent-ui-stream-http.js";
import {
  createScriptedAgentFlow,
  scriptedAgentMarkerDirectoryVariable,
} from "../fixtures/scripted-agent-runner.js";

// Compile-time proof that the adapter's types are AI SDK protocol types.
export const uiMessageChunkCompatibility = (chunk: AgentUIMessageChunk): UIMessageChunk => chunk;
export const uiMessageCompatibility = (messages: AgentUIMessage[]): UIMessage[] => messages;

const workerEntryModulePath = fileURLToPath(
  new URL("../fixtures/scripted-agent-worker.js", import.meta.url),
);

interface ScriptedAgentUIStack {
  readonly flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  readonly markerDirectory: string;
  readonly sessions: AgentSessionTestClient;
  readonly server: AgentUIStreamTestServer;
}

describe("agent UI streams with an in-process Worker", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  const stacks = new Map<string, ScriptedAgentUIStack>();

  before(async () => {
    engine = await PrivateDexEngine.start();
    // Each test gets its own Flow type, so its FileGates are its own.
    const flowsByName = new Map<string, { flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>; markerDirectory: string }>();
    for (const name of [
      "Chat",
      "Reconnect",
      "CleanClose",
      "TurnEnd",
      "TurnFailure",
      "ApprovedReplay",
      "Reload",
      "Events",
      "Question",
    ]) {
      const markerDirectory = mkdtempSync(join(engine.stateDirectory, `markers-${name}-`));
      flowsByName.set(name, {
        flow: createScriptedAgentFlow(markerDirectory, {
          flowType: `UIStream${name}Agent`,
          ...(name === "Question" ? { builtInTools: ["ask_user"] as const } : {}),
        }),
        markerDirectory,
      });
    }
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [...flowsByName.values()].map(({ flow }) => flow),
      isWorkerInProcess: true,
    });
    for (const [name, { flow, markerDirectory }] of flowsByName) {
      stacks.set(name, {
        flow,
        markerDirectory,
        sessions: new AgentSessionTestClient(environment.client, flow),
        server: await AgentUIStreamTestServer.start(environment.client, flow),
      });
    }
  });

  after(async () => {
    for (const stack of stacks.values()) {
      await stack.server.close();
    }
    await environment?.close();
    await engine?.stop();
  });

  function stack(name: string): ScriptedAgentUIStack {
    const found = stacks.get(name);
    assert.ok(found !== undefined, `no stack ${name}`);
    return found;
  }

  test("useChat streams a turn with a read tool, and the stored messages match the streamed ones", async () => {
    const { sessions, server } = stack("Chat");
    const sessionId = newSessionId("chat");
    await sessions.startSession(sessionId);
    const idleResponse = await fetch(`${server.baseUrl}/api/chat/${sessionId}/stream`);
    assert.equal(idleResponse.status, 204);
    const invalidTokenResponse = await fetch(`${server.baseUrl}/api/chat/${sessionId}/stream`, {
      headers: { [agentResumeTokenHeaderName]: "garbage" },
    });
    assert.equal(invalidTokenResponse.status, 400);

    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({ sessionId, baseUrl: server.baseUrl, recordingFetch });
    await chat.sendMessage({ text: "call get_weather" });

    assert.equal(chat.status, "ready");
    const [postResponse] = recordingFetch.responses;
    assert.equal(postResponse?.headers.get("x-vercel-ai-ui-message-stream"), "v1");
    assert.deepEqual(protocolChunkTypes(postResponse?.chunks ?? []), [
      "start",
      "start-step",
      "tool-input-available",
      "tool-output-available",
      "finish-step",
      "start-step",
      "text-start",
      "text-delta",
      "text-end",
      "finish-step",
      "finish",
    ]);
    const assistantMessage = chat.lastMessage;
    assert.equal(assistantMessage?.id, "turn-1-assistant");
    const expectedParts = [
      "step-start",
      'tool get_weather output-available {"requested_by":"call get_weather"} -> sunny in Paris',
      "step-start",
      "text get_weather -> sunny in Paris",
    ];
    assert.deepEqual(describeUIMessageParts(assistantMessage), expectedParts);

    const stored = await fetchStoredMessages(server, sessionId);
    assert.equal(stored.resumeToken, null);
    assert.deepEqual(
      stored.messages.map((message) => message.id),
      ["message-1", "turn-1-assistant"],
    );
    assert.deepEqual(describeUIMessageParts(stored.messages[1]), expectedParts);
    await validateUIMessages({ messages: [...chat.messages, ...stored.messages] });
  });

  test("a dropped connection resumed with the last resume token continues exactly, with no repeated or missing part", async () => {
    const { sessionId, recordingFetch, chat, reference } = await interruptTransferTurn(
      stack("Reconnect"),
      "network-error",
    );
    assert.equal(chat.status, "ready");
    const [firstResponse, resumedResponse] = recordingFetch.responses;
    assert.equal(resumedResponse?.method, "GET");
    assert.equal((resumedResponse?.chunks[0] as { type?: string } | undefined)?.type, "tool-output-available");
    assert.deepEqual(
      withoutResumeTokens([...(firstResponse?.chunks ?? []), ...(resumedResponse?.chunks ?? [])]),
      withoutResumeTokens(reference),
    );
    assert.deepEqual(describeUIMessageParts(await readFinalMessage(reference as UIMessageChunk[])), transferParts);
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), transferParts);
    assert.equal(chat.messages.filter((message) => message.role === "assistant").length, 1);
    assert.deepEqual(new InvocationLog(join(stack("Reconnect").markerDirectory, "transfer_funds.log")).names(), [
      `${sessionId} turn-1-transfer_funds attempt 1`,
    ]);
    const stored = await fetchStoredMessages(stack("Reconnect").server, sessionId);
    assert.deepEqual(describeUIMessageParts(stored.messages.at(-1)), transferParts);
  });

  test("a cleanly closed stream resumed with its token replays the turn into the same message", async () => {
    const { recordingFetch, chat, reference } = await interruptTransferTurn(stack("CleanClose"), "clean-close");
    assert.equal(chat.status, "ready");
    const resumedResponse = recordingFetch.responses[1];
    assert.equal((resumedResponse?.chunks[0] as { type?: string } | undefined)?.type, "start");
    assert.deepEqual(withoutResumeTokens(resumedResponse?.chunks ?? []), withoutResumeTokens(reference));
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), transferParts);
    assert.deepEqual(
      chat.messages.map((message) => message.role),
      ["user", "assistant"],
    );
  });

  test("a connection cut between two parts of one event resumes without repeating either", async () => {
    const { sessions, server } = stack("Chat");
    const sessionId = newSessionId("mid-event-cut");
    await sessions.startSession(sessionId);
    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({
      sessionId,
      baseUrl: server.baseUrl,
      recordingFetch,
      isContinuationEnabled: true,
    });
    // The second model call's first event emits finish-step, start-step, text-start, and text-delta.
    recordingFetch.cutBeforeChunk((chunk) => (chunk as { type?: string }).type === "text-start", "network-error");
    await chat.sendMessage({ text: "call get_weather" });
    assert.equal(chat.status, "error");
    assert.deepEqual(protocolChunkTypes(recordingFetch.responses[0]?.chunks ?? []).slice(-2), [
      "finish-step",
      "start-step",
    ]);

    await chat.resumeStream();

    assert.equal(chat.status, "ready");
    assert.equal(protocolChunkTypes(recordingFetch.responses[1]?.chunks ?? [])[0], "text-start");
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), [
      "step-start",
      'tool get_weather output-available {"requested_by":"call get_weather"} -> sunny in Paris',
      "step-start",
      "text get_weather -> sunny in Paris",
    ]);
  });

  for (const { eventType, stackName, stepExecutionId, payload } of [
    { eventType: "turn_completed", stackName: "TurnEnd", stepExecutionId: "RunModel-1", payload: { message_count: 0 } },
    {
      eventType: "turn_failed",
      stackName: "TurnFailure",
      stepExecutionId: "RecordTurnFailure-1",
      payload: { error: "never committed" },
    },
  ]) {
    test(`a ${eventType} event that never commits does not finish the stream`, async () => {
      const { flow, markerDirectory, sessions } = stack(stackName);
      const sessionId = newSessionId("uncommitted-end");
      await sessions.startSession(sessionId, "call lookup_a");
      const lookupsGate = new FileGate(markerDirectory, "lookups");
      await lookupsGate.waitUntilReached();
      const streaming = collectChunks(
        streamAgentUIMessageChunks({ client: environment.client, flow, flowId: sessionId, pollTimeoutMs: 1_000 }),
      );
      // An end event whose Step never commits, as when the Worker dies right after writing it.
      await environment.client.writeStream(sessionId, flow.agentEvents, "uncommitted-step", {
        type: eventType,
        turnId: "turn-1",
        attempt: 1,
        stepExecutionId,
        payload: JSON.stringify(payload),
      });
      // Outlast the 3-second commit wait before the turn really ends.
      await delay(4_000);
      lookupsGate.release();
      const chunks = await streaming;

      assert.deepEqual(
        chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
        [{ type: "finish", finishReason: "stop" }],
      );
      assert.deepEqual(describeUIMessageParts(await readFinalMessage(chunks as UIMessageChunk[])), [
        "step-start",
        'tool lookup_a output-available {"requested_by":"call lookup_a"} -> lookup_a found',
        "step-start",
        "text lookup_a -> lookup_a found",
      ]);
    });
  }

  test("a question interrupt streams as event data, not as an approval request, and its answer ends the turn", async () => {
    const { flow, sessions } = stack("Question");
    const sessionId = newSessionId("question");
    await sessions.startSession(sessionId, "call ask_user");
    const question = await sessions.waitForPendingInterrupt(sessionId, "ask_user");
    // The stream replays the turn from its first event; a question must not pause it like an approval.
    const streaming = collectChunks(
      streamAgentUIMessageChunks({ client: environment.client, flow, flowId: sessionId, pollTimeoutMs: 1_000 }),
    );
    await delay(2_000);
    assert.equal(await sessions.answerQuestion(sessionId, question.interruptId, "yes"), true);
    const chunks = await streaming;

    assert.deepEqual(chunks.filter((chunk) => chunk.type === "tool-approval-request"), []);
    const interruptEvents = chunks.flatMap((chunk) => {
      const data = (chunk as { type: string; data?: { type?: string; payload?: string } }).data;
      return chunk.type === "data-agent-event" && data?.type?.startsWith("interrupt_") === true
        ? [[data.type, (JSON.parse(data.payload ?? "{}") as { kind?: string }).kind]]
        : [];
    });
    assert.deepEqual(interruptEvents, [
      ["interrupt_raised", "question"],
      ["interrupt_resolved", "question"],
    ]);
    assert.deepEqual(
      chunks.filter((chunk) => chunk.type === "finish" || chunk.type === "error"),
      [{ type: "finish", finishReason: "stop" }],
    );
  });

  test("a reconnect after an approval, while the approved write runs, does not ask for approval again", async () => {
    const { markerDirectory, server, sessions } = stack("ApprovedReplay");
    const sessionId = newSessionId("approved-replay");
    await sessions.startSession(sessionId, "call charge_card");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
    const chargeGate = new FileGate(markerDirectory, "charge_card");
    await chargeGate.waitUntilReached();
    assert.equal((await sessions.readToolCall(sessionId, interrupt.toolCallId))?.status, "approved");
    const initial = await fetchStoredMessages(server, sessionId);
    const expectedPrefix = [
      "start",
      "start-step",
      "tool-input-available",
      "tool-approval-request",
      "tool-approval-response",
    ];

    // A page reload resumes with the snapshot's token; a reconnect without one replays the active turn.
    const reloadAbort = new AbortController();
    const reloadResponse = await fetch(`${server.baseUrl}/api/chat/${sessionId}/stream`, {
      headers: { [agentResumeTokenHeaderName]: initial.resumeToken ?? "" },
      signal: reloadAbort.signal,
    });
    const reloadChunks = await readUIMessageStreamChunks(
      reloadResponse,
      (chunk) => chunk.type === "tool-approval-response" || chunk.type === "finish",
    );
    reloadAbort.abort();
    assert.deepEqual(protocolChunkTypes(reloadChunks), expectedPrefix);
    const tokenlessResponse = await fetch(`${server.baseUrl}/api/chat/${sessionId}/stream`);
    const tokenlessChunks = readUIMessageStreamChunks(tokenlessResponse);
    await pollUntil(
      async () =>
        (await sessions.listAgentEvents(sessionId)).some((event) => event.type === "tool_call_started")
          ? true
          : undefined,
      { description: "the running write" },
    );
    chargeGate.release();
    const chunks = await tokenlessChunks;

    assert.deepEqual(protocolChunkTypes(chunks).slice(0, expectedPrefix.length), expectedPrefix);
    assert.deepEqual(chunks.at(-1), { type: "finish", finishReason: "stop" });
    assert.deepEqual(describeUIMessageParts(await readFinalMessage(chunks as UIMessageChunk[])), [
      "step-start",
      'tool charge_card output-available {"requested_by":"call charge_card"} -> card charged',
      "step-start",
      "text charge_card -> card charged",
    ]);
  });

  test("an approval sent with the paused stream's resume token continues that stream exactly", async () => {
    const { markerDirectory, server, sessions } = stack("Chat");
    const sessionId = newSessionId("approval-token");
    await sessions.startSession(sessionId);
    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({
      sessionId,
      baseUrl: server.baseUrl,
      recordingFetch,
      sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
      isResumeTokenSentWithApprovals: true,
    });
    await chat.sendMessage({ text: "call send_email" });
    const pausedToken = chat.resumeToken;
    assert.ok(pausedToken !== undefined);
    await chat.addToolApprovalResponse({ id: readApprovalId(chat.lastMessage, "send_email"), approved: true });
    const emailOutput = `email sent for ${sessionId}/turn-1/turn-1-send_email`;
    await waitForAssistantText(chat, `send_email -> ${emailOutput}`);

    const approvalRequest = recordingFetch.responses[1];
    assert.equal(approvalRequest?.method, "POST");
    assert.equal((JSON.parse(approvalRequest?.requestBody ?? "{}") as { resumeToken?: string }).resumeToken, pausedToken);
    assert.equal(protocolChunkTypes(approvalRequest?.chunks ?? [])[0], "tool-output-available");
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), [
      "step-start",
      `tool send_email output-available {"requested_by":"call send_email"} -> ${emailOutput}`,
      "step-start",
      `text send_email -> ${emailOutput}`,
    ]);
    assert.deepEqual(
      new InvocationLog(join(markerDirectory, "send_email.log"))
        .names()
        .filter((name) => name.startsWith(`${sessionId} `)),
      [`${sessionId} turn-1-send_email attempt 1`],
    );
  });

  test("an approval bound to another turn than the active one is not applied", async () => {
    const { flow, sessions } = stack("Chat");
    const sessionId = newSessionId("bound-approval");
    await sessions.startSession(sessionId, "call charge_card");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "charge_card");
    const approvedPart = {
      type: "dynamic-tool",
      toolName: "charge_card",
      toolCallId: interrupt.toolCallId,
      state: "approval-responded",
      approval: { id: interrupt.interruptId, approved: false, reason: "from an old tab" },
    };

    const staleApprovals = readAgentToolApprovalResponses({ id: "turn-9-assistant", parts: [approvedPart] });
    assert.deepEqual(staleApprovals.map((approval) => approval.turnId), ["turn-9"]);
    const staleChunks = await readUIMessageStreamChunks(
      await resolveAgentToolApprovalsWithUIMessageStream({
        client: environment.client,
        flow,
        flowId: sessionId,
        approvals: staleApprovals,
        pollTimeoutMs: 1_000,
      }),
    );
    assert.deepEqual(withoutResumeTokens(staleChunks), [
      {
        type: "tool-output-error",
        toolCallId: interrupt.toolCallId,
        errorText: "The approval is no longer pending.",
        dynamic: true,
      },
      { type: "finish", finishReason: "other" },
    ]);
    assert.deepEqual(
      (await sessions.readPendingInterrupts(sessionId)).map((pending) => pending.interruptId),
      [interrupt.interruptId],
    );

    const currentApprovals = readAgentToolApprovalResponses({ id: "turn-1-assistant", parts: [approvedPart] });
    const currentChunks = await readUIMessageStreamChunks(
      await resolveAgentToolApprovalsWithUIMessageStream({
        client: environment.client,
        flow,
        flowId: sessionId,
        approvals: currentApprovals,
        pollTimeoutMs: 1_000,
      }),
    );
    assert.deepEqual(currentChunks.at(-1), { type: "finish", finishReason: "stop" });
    assert.equal((await sessions.getAgentSnapshot(sessionId)).messages.at(-1)?.text, "charge_card -> from an old tab");
  });

  test("an approval answered after another client settled it does not resend in a loop", async () => {
    const { markerDirectory, server, sessions } = stack("Chat");
    const sessionId = newSessionId("stale-approval");
    await sessions.startSession(sessionId);
    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({
      sessionId,
      baseUrl: server.baseUrl,
      recordingFetch,
      sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
    });
    await chat.sendMessage({ text: "call charge_card" });
    const approvalId = readApprovalId(chat.lastMessage, "charge_card");
    assert.equal(await sessions.resolveInterrupt(sessionId, approvalId, false, "declined elsewhere"), true);
    await sessions.waitForCompletedTurns(sessionId, 1);
    const requestCount = recordingFetch.responses.length;

    await chat.addToolApprovalResponse({ id: approvalId, approved: true });
    await pollUntil(
      async () => (chat.status === "ready" && recordingFetch.responses.length > requestCount ? true : undefined),
      { description: "the answer to the stale approval" },
    );
    // A browser stuck in approval-responded would send the answer again at once.
    await delay(2_000);

    assert.equal(recordingFetch.responses.length, requestCount + 1);
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), [
      "step-start",
      'tool charge_card output-available {"requested_by":"call charge_card"} -> declined elsewhere',
    ]);
    assert.deepEqual(recordingFetch.responses.at(-1)?.chunks.at(-1), { type: "finish", finishReason: "other" });
    assert.deepEqual(
      new InvocationLog(join(markerDirectory, "charge_card.log"))
        .names()
        .filter((name) => name.startsWith(`${sessionId} `)),
      [],
    );
  });

  test("approval decisions from the browser are deduplicated, capped, and limited to pending interrupts", async () => {
    const { flow, markerDirectory, sessions } = stack("Chat");
    const sessionId = newSessionId("untrusted-approvals");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    const ghostApprovals = Array.from({ length: 150 }, (_, index) => ({ approvalId: `ghost-${index}`, approved: true }));

    const response = await resolveAgentToolApprovalsWithUIMessageStream({
      client: environment.client,
      flow,
      flowId: sessionId,
      approvals: [
        { approvalId: interrupt.interruptId, approved: true },
        { approvalId: interrupt.interruptId, approved: false, reason: "a forged second answer" },
        { approvalId: "bad/id", approved: true },
        ...ghostApprovals,
      ],
      pollTimeoutMs: 1_000,
    });
    const chunks = await readUIMessageStreamChunks(response);

    const staleToolCallIds = chunks
      .filter((chunk) => chunk.type === "tool-output-error")
      .map((chunk) => (chunk as { toolCallId: string }).toolCallId);
    assert.deepEqual(
      staleToolCallIds,
      ghostApprovals.slice(0, 97).map((approval) => approval.approvalId),
    );
    assert.deepEqual(chunks.at(-1), { type: "finish", finishReason: "stop" });
    const snapshot = await sessions.getAgentSnapshot(sessionId);
    assert.equal(
      snapshot.messages.at(-1)?.text,
      `send_email -> email sent for ${sessionId}/turn-1/turn-1-send_email`,
    );
    assert.deepEqual(
      new InvocationLog(join(markerDirectory, "send_email.log"))
        .names()
        .filter((name) => name.startsWith(`${sessionId} `)),
      [`${sessionId} turn-1-send_email attempt 1`],
    );
  });

  test("a tool approval round trip through useChat runs the approved write once and never runs a rejected one", async () => {
    const { markerDirectory, server, sessions } = stack("Chat");
    const sessionId = newSessionId("approval");
    await sessions.startSession(sessionId);
    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({
      sessionId,
      baseUrl: server.baseUrl,
      recordingFetch,
      sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
    });

    await chat.sendMessage({ text: "call send_email and get_weather" });
    assert.deepEqual(recordingFetch.responses[0]?.chunks.at(-1), { type: "finish", finishReason: "tool-calls" });
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), [
      "step-start",
      'tool send_email approval-requested {"requested_by":"call send_email and get_weather"}',
      'tool get_weather output-available {"requested_by":"call send_email and get_weather"} -> sunny in Paris',
    ]);
    const sessionInvocations = (toolName: string) =>
      new InvocationLog(join(markerDirectory, `${toolName}.log`))
        .names()
        .filter((name) => name.startsWith(`${sessionId} `));
    assert.deepEqual(sessionInvocations("send_email"), []);
    await chat.addToolApprovalResponse({ id: readApprovalId(chat.lastMessage, "send_email"), approved: true });
    const emailOutput = `email sent for ${sessionId}/turn-1/turn-1-send_email`;
    await waitForAssistantText(chat, `send_email -> ${emailOutput} | get_weather -> sunny in Paris`);

    const approvedParts = describeUIMessageParts(chat.lastMessage);
    assert.deepEqual(approvedParts, [
      "step-start",
      `tool send_email output-available {"requested_by":"call send_email and get_weather"} -> ${emailOutput}`,
      'tool get_weather output-available {"requested_by":"call send_email and get_weather"} -> sunny in Paris',
      "step-start",
      `text send_email -> ${emailOutput} | get_weather -> sunny in Paris`,
    ]);
    assert.equal(recordingFetch.responses[1]?.method, "POST");
    assert.deepEqual(sessionInvocations("send_email"), [`${sessionId} turn-1-send_email attempt 1`]);
    // The continuation knows where the turn started, so a reconnect with its token replays the whole turn.
    const continuationToken = recordingFetch.responses[1]?.chunks.find(
      (chunk) => (chunk as { type?: string }).type === "data-agent-resume-token",
    ) as { data: string } | undefined;
    assert.notEqual(decodeAgentUIStreamCursor(continuationToken?.data ?? "").turnStartResumeToken, null);

    await chat.sendMessage({ text: "call charge_card" });
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), [
      "step-start",
      'tool charge_card approval-requested {"requested_by":"call charge_card"}',
    ]);
    await chat.addToolApprovalResponse({
      id: readApprovalId(chat.lastMessage, "charge_card"),
      approved: false,
      reason: "not today",
    });
    await waitForAssistantText(chat, "charge_card -> not today");
    assert.equal(chat.lastMessage?.id, "turn-2-assistant");
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), [
      "step-start",
      'tool charge_card output-denied {"requested_by":"call charge_card"}',
      "step-start",
      "text charge_card -> not today",
    ]);
    assert.deepEqual(sessionInvocations("charge_card"), []);

    const stored = await fetchStoredMessages(server, sessionId);
    assert.deepEqual(describeUIMessageParts(stored.messages[1]), approvedParts);
  });

  test("a page reload replays the active turn from the snapshot's resume token into the stored message", async () => {
    const { markerDirectory, server, sessions } = stack("Reload");
    const sessionId = newSessionId("reload");
    await sessions.startSession(sessionId, "call lookup_a");
    const lookupsGate = new FileGate(markerDirectory, "lookups");
    await lookupsGate.waitUntilReached();

    const initial = await fetchStoredMessages(server, sessionId);
    assert.notEqual(initial.resumeToken, null);
    assert.deepEqual(
      initial.messages.map((message) => message.id),
      ["message-1", "turn-1-assistant"],
    );
    assert.deepEqual(describeUIMessageParts(initial.messages[1]), [
      "step-start",
      'tool lookup_a input-available {"requested_by":"call lookup_a"}',
    ]);

    // A token whose Dex position belongs to another session restarts from the turn's listed start.
    const otherSessionId = newSessionId("reload-other");
    await sessions.startSession(otherSessionId, "hello");
    await sessions.waitForCompletedTurns(otherSessionId, 1);
    const otherPage = await environment.client.listStreamMessages(otherSessionId, stack("Reload").flow.agentEvents, 1);
    const forgedCursor = decodeAgentUIStreamCursor(initial.resumeToken ?? "");
    forgedCursor.eventResumeToken = otherPage.messages[0]?.resumeToken ?? "";
    forgedCursor.turnStartResumeToken = forgedCursor.eventResumeToken;
    const forgedAbort = new AbortController();
    const forgedResponse = await fetch(`${server.baseUrl}/api/chat/${sessionId}/stream`, {
      headers: { [agentResumeTokenHeaderName]: encodeAgentUIStreamCursor(forgedCursor) },
      signal: forgedAbort.signal,
    });
    assert.equal(forgedResponse.status, 200);
    const forgedChunks = await readUIMessageStreamChunks(
      forgedResponse,
      (chunk) => chunk.type === "tool-input-available",
    );
    forgedAbort.abort();
    assert.deepEqual(protocolChunkTypes(forgedChunks), ["start", "start-step", "tool-input-available"]);

    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({
      sessionId,
      baseUrl: server.baseUrl,
      recordingFetch,
      messages: initial.messages,
      resumeToken: initial.resumeToken ?? undefined,
    });
    const resuming = chat.resumeStream();
    await pollUntil(async () => ((recordingFetch.responses[0]?.chunks.length ?? 0) > 0 ? true : undefined), {
      description: "the replayed turn",
    });
    lookupsGate.release();
    await resuming;

    const expectedParts = [
      "step-start",
      'tool lookup_a output-available {"requested_by":"call lookup_a"} -> lookup_a found',
      "step-start",
      "text lookup_a -> lookup_a found",
    ];
    assert.equal((recordingFetch.responses[0]?.chunks[0] as { type?: string } | undefined)?.type, "start");
    assert.deepEqual(
      chat.messages.map((message) => message.id),
      ["message-1", "turn-1-assistant"],
    );
    assert.deepEqual(describeUIMessageParts(chat.lastMessage), expectedParts);
    const stored = await fetchStoredMessages(server, sessionId);
    assert.equal(stored.resumeToken, null);
    assert.deepEqual(describeUIMessageParts(stored.messages[1]), expectedParts);
  });

  test("the plain event stream resumes from Last-Event-ID with no repeated or missing event", async () => {
    const { flow, markerDirectory, server, sessions } = stack("Events");
    const sessionId = newSessionId("events");
    await sessions.startSession(sessionId, "hello");
    await sessions.waitForCompletedTurns(sessionId, 1);
    const snapshotResponse = (await (await fetch(`${server.baseUrl}/api/sessions/${sessionId}/snapshot`)).json()) as {
      readonly snapshot: { readonly state: { readonly turn_count: number }; readonly messages: unknown[] };
      readonly resume_token: string;
    };
    assert.equal(snapshotResponse.snapshot.state.turn_count, 1);
    assert.equal(snapshotResponse.snapshot.messages.length, 2);
    const startToken = snapshotResponse.resume_token;
    assert.notEqual(startToken, "");

    const firstConnection = new AbortController();
    const firstEvents = readServerSentEvents(
      `${server.baseUrl}/api/sessions/${sessionId}/events?resumeToken=${encodeURIComponent(startToken)}`,
      {},
      firstConnection.signal,
      (event) => event.data.type === "tool_call_started",
    );
    assert.equal(await sessions.sendUserMessage(sessionId, "call lookup_a"), true);
    const firstFrames = await firstEvents;
    firstConnection.abort();
    new FileGate(markerDirectory, "lookups").release();
    await sessions.waitForCompletedTurns(sessionId, 2);

    const lastEventId = firstFrames.at(-1)?.id ?? "";
    const secondFrames = await readServerSentEvents(
      `${server.baseUrl}/api/sessions/${sessionId}/events?resumeToken=${encodeURIComponent(startToken)}`,
      { "last-event-id": lastEventId },
      new AbortController().signal,
      (event) => event.data.type === "turn_completed",
    );

    const page = await environment.client.listStreamMessages(sessionId, flow.agentEvents, 500);
    const retained = [...page.messages].reverse();
    const startIndex = retained.findIndex((message) => message.resumeToken === startToken);
    const expected = retained.slice(startIndex + 1);
    const received = [...firstFrames, ...secondFrames];
    assert.deepEqual(
      received.map((frame) => frame.id),
      expected.map((message) => message.resumeToken),
    );
    assert.deepEqual(
      received.map((frame) => frame.data),
      expected.map((message) => convertAgentEventToJson(message.value)),
    );

    // A token of another session answers 400, so an EventSource stops reconnecting with it.
    const otherSessionId = newSessionId("events-other");
    await sessions.startSession(otherSessionId, "hello");
    await sessions.waitForCompletedTurns(otherSessionId, 1);
    const otherPage = await environment.client.listStreamMessages(otherSessionId, flow.agentEvents, 1);
    const rejected = await fetch(`${server.baseUrl}/api/sessions/${sessionId}/events`, {
      headers: { "last-event-id": otherPage.messages[0]?.resumeToken ?? "" },
    });
    assert.equal(rejected.status, 400);
  });
});

describe("agent UI streams with a Worker killed with SIGKILL", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let sessions: AgentSessionTestClient;
  const workers: KillableWorkerProcess[] = [];

  before(async () => {
    engine = await PrivateDexEngine.start();
    flow = createScriptedAgentFlow(join(engine.stateDirectory, "client-unused-markers"));
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
  });

  afterEach(async () => {
    for (const worker of workers.splice(0)) {
      await worker.kill();
    }
  });

  after(async () => {
    await environment?.close();
    await engine?.stop();
  });

  async function startWorker(markerDirectory: string): Promise<KillableWorkerProcess> {
    const worker = await KillableWorkerProcess.start({
      entryModulePath: workerEntryModulePath,
      serverAddress: engine.serverAddress,
      logPath: join(markerDirectory, "worker.log"),
      environment: { [scriptedAgentMarkerDirectoryVariable]: markerDirectory },
    });
    workers.push(worker);
    return worker;
  }

  test("a Worker killed during RunModel resets the step and keeps only the new attempt's text", async () => {
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const firstWorker = await startWorker(markerDirectory);
    const sessionId = newSessionId("model-retry");
    await sessions.startSession(sessionId, "slow stream");
    await sessions.waitForAgentState(sessionId, (state) => state.activeTurnId === "turn-1", "turn-1 active");
    const chunks = collectChunks(
      streamAgentUIMessageChunks({ client: environment.client, flow, flowId: sessionId, pollTimeoutMs: 1_000 }),
    );
    await pollUntil(
      async () =>
        (await sessions.listAgentEvents(sessionId)).some((event) => event.type === "text_delta") ? true : undefined,
      { description: "the first attempt's text" },
    );

    await firstWorker.kill();
    new FileGate(markerDirectory, "turn").release();
    await startWorker(markerDirectory);

    const streamedChunks = await chunks;
    const chunkTypes = protocolChunkTypes(streamedChunks);
    const resetIndex = chunkTypes.indexOf("reset-step");
    assert.ok(resetIndex > chunkTypes.indexOf("text-delta"), `reset-step after the first text: ${chunkTypes.join(", ")}`);
    assert.deepEqual(chunkTypes.slice(0, 3), ["start", "start-step", "text-start"]);
    assert.deepEqual(chunkTypes.slice(-3), ["text-end", "finish-step", "finish"]);
    const message = await readFinalMessage(streamedChunks as UIMessageChunk[]);
    assert.deepEqual(describeUIMessageParts(message), [
      "step-start",
      "text partial text of attempt 2slow turn done",
    ]);
    await validateUIMessages({ messages: [message] });
  });
});

describe("agent UI streams with the Stream Store disabled", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let uiStack: ScriptedAgentUIStack;

  before(async () => {
    const configDirectory = mkdtempSync(join(tmpdir(), "dex-agents-disabled-streams-"));
    const configPath = join(configDirectory, "server-zd.yaml");
    writeFileSync(configPath, "streamStore:\n  backend: disabled\n");
    engine = await PrivateDexEngine.start({ serverZdConfigPath: configPath });
    const markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    const flow = createScriptedAgentFlow(markerDirectory, { flowType: "UIStreamDisabledAgent" });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: true,
    });
    uiStack = {
      flow,
      markerDirectory,
      sessions: new AgentSessionTestClient(environment.client, flow),
      server: await AgentUIStreamTestServer.start(environment.client, flow),
    };
  });

  after(async () => {
    await uiStack?.server.close();
    await environment?.close();
    await engine?.stop();
  });

  test("useChat finishes a turn from committed state when no event reaches the Stream", async () => {
    const { server, sessions } = uiStack;
    const sessionId = newSessionId("disabled");
    await sessions.startSession(sessionId);
    const recordingFetch = new RecordingFetch();
    const chat = new AgentUIStreamTestChat({ sessionId, baseUrl: server.baseUrl, recordingFetch });

    await chat.sendMessage({ text: "call get_weather" });

    assert.equal(chat.status, "ready");
    assert.equal(recordingFetch.responses[0]?.status, 200);
    assert.deepEqual(protocolChunkTypes(recordingFetch.responses[0]?.chunks ?? []), ["finish"]);
    assert.deepEqual(recordingFetch.responses[0]?.chunks.at(-1), { type: "finish", finishReason: "other" });
    const stored = await fetchStoredMessages(server, sessionId);
    assert.equal(stored.resumeToken, null);
    assert.deepEqual(describeUIMessageParts(stored.messages[1]), [
      "step-start",
      'tool get_weather output-available {"requested_by":"call get_weather"} -> sunny in Paris',
      "step-start",
      "text get_weather -> sunny in Paris",
    ]);
    assert.equal((await fetch(`${server.baseUrl}/api/chat/${sessionId}/stream`)).status, 204);
    const snapshotResponse = await fetch(`${server.baseUrl}/api/sessions/${sessionId}/snapshot`);
    assert.equal(((await snapshotResponse.json()) as { resume_token: string }).resume_token, "");
  });
});

describe("agent UI streams with a Worker whose only Step slot is busy", () => {
  let engine: PrivateDexEngine;
  let environment: AgentTestEnvironment;
  let workerBlobCache: BlobCache;
  let worker: Worker;
  let flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
  let markerDirectory: string;
  let sessions: AgentSessionTestClient;

  before(async () => {
    engine = await PrivateDexEngine.start();
    markerDirectory = mkdtempSync(join(engine.stateDirectory, "markers-"));
    flow = createScriptedAgentFlow(markerDirectory, { flowType: "UIStreamBusyWorkerAgent" });
    environment = await AgentTestEnvironment.start({
      serverAddress: engine.serverAddress,
      flows: [flow],
      isWorkerInProcess: false,
    });
    sessions = new AgentSessionTestClient(environment.client, flow);
    workerBlobCache = openBlobCache({
      directory: mkdtempSync(join(engine.stateDirectory, "worker-cache-")),
      maxBytes: 64 * 1024 * 1024,
    });
    // One Step slot, plus one reserved for RPCs, so resolve_interrupt runs while a Step holds the other.
    worker = new Worker(environment.registry, workerBlobCache, {
      serverAddress: engine.serverAddress,
      pull: { appWorkerID: `busy-worker-${randomUUID()}`, maxConcurrentTasks: 2, reservedRpcTasks: 1 },
    });
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
  });

  after(async () => {
    await worker?.close();
    workerBlobCache?.close();
    await environment?.close();
    await engine?.stop();
  });

  test("a reconnect after resolve_interrupt, before the approval Step commits, keeps reading instead of pausing", async () => {
    const sessionId = newSessionId("held-approval");
    await sessions.startSession(sessionId, "call send_email");
    const interrupt = await sessions.waitForPendingInterrupt(sessionId, "send_email");
    // A slow model call of another session holds the only Step slot, so the approval Step cannot run.
    const turnGate = new FileGate(markerDirectory, "turn");
    await sessions.startSession(newSessionId("slot-holder"), "slow");
    await turnGate.waitUntilReached();
    try {
      assert.equal(await sessions.resolveInterrupt(sessionId, interrupt.interruptId, true), true);
      assert.deepEqual(await sessions.readPendingInterrupts(sessionId), []);
      assert.equal((await sessions.readToolCall(sessionId, interrupt.toolCallId))?.status, "awaiting_approval");

      const streaming = collectChunks(
        streamAgentUIMessageChunks({ client: environment.client, flow, flowId: sessionId, pollTimeoutMs: 1_000 }),
      );
      const streamEndedWhileHeld = await Promise.race([
        streaming.then(() => true),
        delay(3_000).then(() => false),
      ]);

      assert.equal(streamEndedWhileHeld, false, "the stream paused on an approval that resolve_interrupt removed");
      assert.equal((await sessions.readToolCall(sessionId, interrupt.toolCallId))?.status, "awaiting_approval");
      turnGate.release();
      const chunks = await streaming;
      assert.deepEqual(
        protocolChunkTypes(chunks).filter((type) => type.startsWith("tool-")),
        ["tool-input-available", "tool-approval-request", "tool-approval-response", "tool-output-available"],
      );
      assert.deepEqual(chunks.at(-1), { type: "finish", finishReason: "stop" });
    } finally {
      turnGate.release();
    }
  });
});

describe("agent snapshot JSON for browsers", () => {
  test("leaves out the fields only the session's Steps use, with the same text as the Python core", () => {
    const snapshot: AgentSnapshot = {
      state: { status: "running_tools", turnCount: 1, activeTurnId: "turn-2", nextMessageSequence: 4, lastTurnError: null },
      messages: [
        {
          sequence: 3,
          turnId: "turn-2",
          role: "user",
          text: "refund order 42",
          toolCallId: null,
          toolName: null,
          toolArguments: null,
          toolOutput: null,
          frameworkItem: '{"instructions":"Never refund more than $50."}',
          createdAt: "2026-11-01T12:00:00.000Z",
        },
      ],
      pendingInterrupts: [
        {
          interruptId: "call-1-input-2",
          kind: "question",
          toolCallId: "call-1",
          toolName: "refund_order",
          arguments: '{"order_id":"42"}',
          createdAt: "2026-11-01T12:00:01.000Z",
          deadline: null,
          question: "Refund to the card?",
          choices: ["yes", "no"],
          allowsFreeText: false,
        },
      ],
      toolCalls: [
        {
          toolCallId: "call-1",
          turnId: "turn-2",
          toolName: "refund_order",
          arguments: '{"order_id":"42"}',
          effect: "idempotent_write",
          requiresApproval: false,
          status: "awaiting_answer",
          output: null,
          error: null,
          rejectionMessage: null,
          deferralOrder: 0,
          toolSourceReference: '{"server":"payments","tool":"refund_order"}',
          traceContext: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
          waitUntil: null,
          inputContinuation: '{"refund_draft":"rf_1"}',
          inputAnswers: [{ interruptId: "call-1-input-1", question: "Which order?", answer: "42" }],
          inputQuestion: "Refund to the card?",
          inputChoices: ["yes", "no"],
          inputAllowsFreeText: false,
        },
      ],
      usage: { modelCalls: 1, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, reasoningTokens: 0 },
      queuedMessageCount: 0,
    };

    // The Python core's test_snapshot_json_leaves_out_the_fields_only_the_session_steps_use expects this text.
    assert.equal(
      JSON.stringify(convertAgentSnapshotToJson(snapshot)),
      '{"state":{"status":"running_tools","turn_count":1,"active_turn_id":"turn-2",' +
        '"next_message_sequence":4,"last_turn_error":null},' +
        '"messages":[{"sequence":3,"turn_id":"turn-2","role":"user","text":"refund order 42",' +
        '"tool_call_id":null,"tool_name":null,"tool_arguments":null,"tool_output":null,' +
        '"created_at":"2026-11-01T12:00:00.000Z"}],' +
        '"pending_interrupts":[{"interrupt_id":"call-1-input-2","kind":"question",' +
        '"tool_call_id":"call-1","tool_name":"refund_order","arguments":"{\\"order_id\\":\\"42\\"}",' +
        '"created_at":"2026-11-01T12:00:01.000Z","deadline":null,"question":"Refund to the card?",' +
        '"choices":["yes","no"],"allows_free_text":false}],' +
        '"tool_calls":[{"tool_call_id":"call-1","turn_id":"turn-2","tool_name":"refund_order",' +
        '"arguments":"{\\"order_id\\":\\"42\\"}","effect":"idempotent_write","requires_approval":false,' +
        '"status":"awaiting_answer","output":null,"error":null,"rejection_message":null,' +
        '"deferral_order":0,"wait_until":null,"input_question":"Refund to the card?",' +
        '"input_choices":["yes","no"],"input_allows_free_text":false}],' +
        '"usage":{"model_calls":1,"input_tokens":10,"output_tokens":5,"cached_input_tokens":0,' +
        '"reasoning_tokens":0},"queued_message_count":0}',
    );
  });
});

describe("agent UI stream resume tokens", () => {
  test("stay within the size limit by dropping finished tool calls, and refuse to grow past it", () => {
    const cursor = createAgentUIStreamCursor("dex-token", "turn-1", true);
    for (let index = 0; index < 3_000; index += 1) {
      cursor.toolCalls.set(`finished-tool-call-${index}-${"x".repeat(24)}`, "finished");
    }
    cursor.toolCalls.set("open-tool-call", "approval_requested");

    const resumeToken = encodeAgentUIStreamCursor(cursor);

    assert.ok(resumeToken.length <= maximumAgentUIStreamResumeTokenLength);
    assert.deepEqual([...decodeAgentUIStreamCursor(resumeToken).toolCalls], [["open-tool-call", "approval_requested"]]);
    for (let index = 0; index < 3_000; index += 1) {
      cursor.toolCalls.set(`open-tool-call-${index}-${"x".repeat(24)}`, "input");
    }
    assert.throws(() => encodeAgentUIStreamCursor(cursor), RangeError);
  });
});

const transferParts = [
  "step-start",
  'tool transfer_funds output-available {"requested_by":"call transfer_funds then pause"} -> funds transferred',
  "step-start",
  "text transfer_funds -> funds transferred",
];

/**
 * Cuts a chat's stream while `transfer_funds` runs, finishes the turn, and resumes the chat, next to an
 * uninterrupted replay of the same turn.
 */
async function interruptTransferTurn(
  uiStack: ScriptedAgentUIStack,
  cut: ResponseCut,
): Promise<{
  readonly sessionId: string;
  readonly recordingFetch: RecordingFetch;
  readonly chat: AgentUIStreamTestChat;
  readonly reference: unknown[];
}> {
  const { markerDirectory, server, sessions } = uiStack;
  const sessionId = newSessionId("interrupted");
  await sessions.startSession(sessionId);
  const recordingFetch = new RecordingFetch();
  const chat = new AgentUIStreamTestChat({
    sessionId,
    baseUrl: server.baseUrl,
    recordingFetch,
    isContinuationEnabled: true,
  });
  const sending = chat.sendMessage({ text: "call transfer_funds then pause" });
  const transferGate = new FileGate(markerDirectory, "transfer_funds");
  await transferGate.waitUntilReached();
  const referenceFetch = new RecordingFetch();
  const referenceChunks = collectReplayedChunks(server, sessionId, referenceFetch);

  // CallTool waits at the gate, so the first response is idle after tool_call_started.
  await pollUntil(
    async () => {
      const chunks = recordingFetch.responses[0]?.chunks ?? [];
      const lastChunk = chunks.at(-1) as { type?: string; data?: string } | undefined;
      const hasStartedTool = chunks.some(
        (chunk) => (chunk as { data?: { type?: string } }).data?.type === "tool_call_started",
      );
      return hasStartedTool && lastChunk?.type === "data-agent-resume-token" && chat.resumeToken === lastChunk.data
        ? true
        : undefined;
    },
    { description: "the first response to reach the running tool call" },
  );
  recordingFetch.disconnect(cut);
  await sending;
  assert.equal(chat.status, cut === "network-error" ? "error" : "ready");

  transferGate.release();
  const turnGate = new FileGate(markerDirectory, "turn");
  await turnGate.waitUntilReached();
  const resuming = chat.resumeStream();
  await pollUntil(async () => (recordingFetch.responses.length === 2 ? true : undefined), {
    description: "the reconnect request",
  });
  turnGate.release();
  await resuming;
  return { sessionId, recordingFetch, chat, reference: await referenceChunks };
}

function withoutResumeTokens(chunks: readonly unknown[]): unknown[] {
  return chunks.filter((chunk) => (chunk as { type?: string }).type !== "data-agent-resume-token");
}

/** Reads the parts of a UI message stream response until `isLastChunk` holds or the stream ends. */
async function readUIMessageStreamChunks(
  response: Response,
  isLastChunk: (chunk: AgentUIMessageChunk) => boolean = () => false,
): Promise<AgentUIMessageChunk[]> {
  assert.ok(response.body !== null);
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  const chunks: AgentUIMessageChunk[] = [];
  let pendingText = "";
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        return chunks;
      }
      pendingText += value;
      let frameEnd = pendingText.indexOf("\n\n");
      while (frameEnd >= 0) {
        const frame = pendingText.slice(0, frameEnd);
        pendingText = pendingText.slice(frameEnd + 2);
        frameEnd = pendingText.indexOf("\n\n");
        if (!frame.startsWith("data: ") || frame === "data: [DONE]") {
          continue;
        }
        const chunk = JSON.parse(frame.slice("data: ".length)) as AgentUIMessageChunk;
        chunks.push(chunk);
        if (isLastChunk(chunk)) {
          return chunks;
        }
      }
    }
  } finally {
    await reader.cancel();
  }
}

function newSessionId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

/** The part types of the UI protocol, without the transient data parts. */
function protocolChunkTypes(chunks: readonly unknown[]): string[] {
  return chunks
    .map((chunk) => String((chunk as { type?: unknown }).type))
    .filter((type) => !type.startsWith("data-"));
}

async function fetchStoredMessages(
  server: AgentUIStreamTestServer,
  sessionId: string,
): Promise<AgentSessionUIMessages> {
  const response = await fetch(`${server.baseUrl}/api/sessions/${sessionId}/messages`);
  assert.equal(response.status, 200);
  return (await response.json()) as AgentSessionUIMessages;
}

/** Reads the active turn through the AI SDK transport, which validates every part. */
async function collectReplayedChunks(
  server: AgentUIStreamTestServer,
  sessionId: string,
  recordingFetch: RecordingFetch,
): Promise<unknown[]> {
  const transport = new DefaultChatTransport<UIMessage>({
    api: `${server.baseUrl}/api/chat`,
    fetch: recordingFetch.fetch,
  });
  const stream = await transport.reconnectToStream({ chatId: sessionId });
  assert.ok(stream !== null, "the active turn has a stream");
  await collectChunks(stream);
  return recordingFetch.responses[0]?.chunks ?? [];
}

async function collectChunks<T>(chunks: AsyncIterable<T>): Promise<T[]> {
  const collected: T[] = [];
  for await (const chunk of chunks) {
    collected.push(chunk);
  }
  return collected;
}

async function readFinalMessage(chunks: readonly UIMessageChunk[]): Promise<UIMessage | undefined> {
  let message: UIMessage | undefined;
  for await (const update of readUIMessageStream({ stream: ReadableStream.from(chunks) })) {
    message = update;
  }
  return message;
}

function readApprovalId(message: UIMessage | undefined, toolName: string): string {
  const part = message?.parts.find(
    (candidate) => candidate.type === "dynamic-tool" && candidate.toolName === toolName,
  );
  assert.ok(part?.type === "dynamic-tool" && part.state === "approval-requested", `${toolName} awaits approval`);
  assert.equal(part.approval.id, part.toolCallId);
  return part.approval.id;
}

async function waitForAssistantText(chat: AgentUIStreamTestChat, text: string): Promise<void> {
  await pollUntil(
    async () =>
      chat.status === "ready" &&
      chat.lastMessage?.parts.some((part) => part.type === "text" && part.text === text) === true
        ? true
        : undefined,
    { description: `assistant text ${text}` },
  );
}

interface ServerSentAgentEvent {
  readonly id: string;
  readonly data: ReturnType<typeof convertAgentEventToJson>;
}

/** Reads `AgentEvents` frames until one matches `isLastEvent`, then closes the connection. */
async function readServerSentEvents(
  url: string,
  headers: Readonly<Record<string, string>>,
  signal: AbortSignal,
  isLastEvent: (event: ServerSentAgentEvent) => boolean,
): Promise<ServerSentAgentEvent[]> {
  const response = await fetch(url, { headers, signal });
  assert.equal(response.headers.get("content-type"), "text/event-stream");
  assert.ok(response.body !== null);
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  const events: ServerSentAgentEvent[] = [];
  let pendingText = "";
  try {
    for (;;) {
      const { done, value } = await reader.read();
      assert.equal(done, false, "the event stream ended early");
      pendingText += value;
      let frameEnd = pendingText.indexOf("\n\n");
      while (frameEnd >= 0) {
        const lines = pendingText.slice(0, frameEnd).split("\n");
        pendingText = pendingText.slice(frameEnd + 2);
        frameEnd = pendingText.indexOf("\n\n");
        const id = lines.find((line) => line.startsWith("id: "))?.slice("id: ".length);
        const data = lines.find((line) => line.startsWith("data: "))?.slice("data: ".length);
        if (id === undefined || data === undefined) {
          continue;
        }
        const event = { id, data: JSON.parse(data) as ServerSentAgentEvent["data"] };
        events.push(event);
        if (isLastEvent(event)) {
          return events;
        }
      }
    }
  } finally {
    await reader.cancel();
  }
}
