/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import type { Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import express from "express";

import { Client, Registry, openBlobCache, type BlobCache } from "@superdurable/dex";
import type { AgentSnapshot } from "@superdurable/dex-agents";
import { AgentSessionTestClient, KillableWorkerProcess, pollUntil } from "@superdurable/dex-agents/testing";

import { loadAiAgentConfig, loadMcpServers } from "../../src/products/ai-agent/ai-agent-config.js";
import { createAiAgentFlow, type AiAgentFlow } from "../../src/products/ai-agent/ai-agent-flow.js";
import { createAiAgentRouter, sessionTokenHeaderName } from "../../src/products/ai-agent/controller.js";

// The demo model needs no key. A small window makes compaction reachable.
const workerEnvironment: Record<string, string> = { DEX_AGENT_MODEL: "mock/dex", DEX_AGENT_CONTEXT_TOKENS: "600" };
Object.assign(process.env, workerEnvironment);

const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
const waitOptions = { timeoutMs: 60_000 };
const sessionKey = "ai-agent-test-key";

let stateDirectory: string;
let flow: AiAgentFlow;
let blobCache: BlobCache;
let client: Client;
let sessions: AgentSessionTestClient;
let worker: KillableWorkerProcess;
let routes: AiAgentRoutes;

test.before(async () => {
  stateDirectory = await mkdtemp(join(tmpdir(), "dex-typescript-examples-ai-agent-"));
  // The Worker posts to Slack through the connector mock and appends each call to this file.
  workerEnvironment.AI_AGENT_TEST_SLACK_CALLS_FILE = join(stateDirectory, "slack-calls.jsonl");
  await writeFile(workerEnvironment.AI_AGENT_TEST_SLACK_CALLS_FILE, "");
  // The test process only starts sessions and calls RPCs, so its Flow needs no Slack connection.
  flow = createAiAgentFlow(loadAiAgentConfig(), undefined);
  blobCache = openBlobCache({ directory: join(stateDirectory, "blobs"), maxBytes: 64 * 1024 * 1024 });
  client = new Client(new Registry([flow]), blobCache, { serverAddress });
  sessions = new AgentSessionTestClient(client, flow);
  worker = await startWorker();
  routes = await AiAgentRoutes.start(client);
});

test.after(async () => {
  await routes.close();
  await worker.stop();
  await client.close();
  blobCache.close();
  await rm(stateDirectory, { recursive: true, force: true });
});

test("aiAgentApprovesAnMcpWriteThroughUseChatRoutes", async () => {
  const session = await routes.startSession();
  const write = '/tool notes__post_message {"channel":"#general","text":"hello"}';
  const request = await routes.postChat(session, [{ id: "user-1", role: "user", parts: [{ type: "text", text: write }] }]);
  assert.match(request, /"finishReason":"tool-calls"/);
  const approvalId = /"tool-approval-request","approvalId":"([^"]+)"/.exec(request)?.[1] ?? "";

  const approval = { type: "dynamic-tool", toolCallId: approvalId, toolName: "notes__post_message", state: "approval-responded" };
  const answer = { id: "turn-1-assistant", role: "assistant", parts: [{ ...approval, input: {}, approval: { id: approvalId, approved: true } }] };
  const continued = await routes.postChat(session, [answer]);
  assert.match(continued, /"type":"tool-output-available"/);
  assert.match(continued, /"finishReason":"stop"/);

  const history = await routes.getJson<{ messages: { tool_output: string | null }[] }>(session, "messages");
  assert.ok(history.messages.some((message) => message.tool_output === "posted to #general: hello"));
});

test("aiAgentRecordsARejectedApproval", async () => {
  const sessionId = await startSession();
  await sessions.sendUserMessage(sessionId, '/tool notes__post_message {"channel":"#ops","text":"no"}');
  const approval = await sessions.waitForPendingInterrupt(sessionId, "notes__post_message", waitOptions);
  assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, false), true);
  await sessions.waitForCompletedTurns(sessionId, 1, waitOptions);
  const history = await sessions.listAgentMessages(sessionId, null, 50);
  assert.equal(history.messages.find((message) => message.role === "tool")?.toolName, "notes__post_message");
});

test("aiAgentPostsAnApprovedSlackThreadReplyOnce", async () => {
  const sessionId = await startSession();
  const replyInput = { channelId: "C0123456789", threadTimestamp: "1710000000.000100", text: "Order 1001 has shipped." };
  await sessions.sendUserMessage(sessionId, `/tool slack_post_thread_reply ${JSON.stringify(replyInput)}`);
  const approval = await sessions.waitForPendingInterrupt(sessionId, "slack_post_thread_reply", waitOptions);
  assert.deepEqual(await readSlackCalls(), []);

  assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, true), true);
  await sessions.waitForCompletedTurns(sessionId, 1, waitOptions);
  const calls = await readSlackCalls();
  assert.equal(calls.length, 1);
  const [providerCall] = calls;
  assert.equal(providerCall?.flowId, sessionId);
  assert.deepEqual(providerCall?.input, replyInput);
  assert.equal(providerCall?.branch, "sent");
  assert.equal(providerCall?.retry, undefined);
  assert.equal(providerCall?.replay, undefined);
  // Slack deduplicates on a key derived from the Call ID, which the tool Step keeps across attempts.
  assert.equal(providerCall?.idempotencyKey, providerCall?.callId);

  const snapshot = await sessions.getAgentSnapshot(sessionId);
  const toolCall = snapshot.toolCalls.find((record) => record.toolName === "slack_post_thread_reply");
  assert.equal(toolCall?.status, "succeeded");
  // A non-idempotent write runs once, in its own single-attempt tool Step.
  assert.equal(toolCall?.effect, "non_idempotent_write");
  assert.equal(toolCall?.requiresApproval, true);
  assert.equal(await sessions.resolveInterrupt(sessionId, approval.interruptId, true), false);
  assert.equal((await readSlackCalls()).length, 1);
});

test("aiAgentReadsAnMcpResource", async () => {
  const sessionId = await startSession();
  await sessions.sendUserMessage(sessionId, '/tool mcp_read_resource {"server":"notes","uri":"notes://all"}');
  await sessions.waitForCompletedTurns(sessionId, 1, waitOptions);
  assert.match(lastText(await sessions.getAgentSnapshot(sessionId)), /durable wait survives Worker restarts/);
});

test("aiAgentAnswersAndDeclinesQuestionsThroughRoutes", async () => {
  const session = await routes.startSession();
  await routes.post(session, "messages", { text: "/choose Where? | #general | #random" });
  const choice = await routes.waitForQuestion(session);
  assert.deepEqual(choice.choices, ["#general", "#random"]);
  assert.equal((await routes.post(session, `questions/${choice.interrupt_id}/answer`, { answer: "#elsewhere" })).status, 409);
  assert.equal((await routes.post(session, `questions/${choice.interrupt_id}/answer`, { answer: "#random" })).status, 200);
  await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);

  await routes.post(session, "messages", { text: "/ask What should the report say?" });
  const freeText = await routes.waitForQuestion(session);
  assert.equal((await routes.post(session, `questions/${freeText.interrupt_id}/answer`, { answer: "Ship it" })).status, 200);
  await sessions.waitForCompletedTurns(session.sessionId, 2, waitOptions);
  assert.match(lastText(await sessions.getAgentSnapshot(session.sessionId)), /Ship it/);

  await routes.post(session, "messages", { text: "/ask Anything else?" });
  const declined = await routes.waitForQuestion(session);
  assert.equal((await routes.post(session, `questions/${declined.interrupt_id}/decline`, {})).status, 200);
  await sessions.waitForCompletedTurns(session.sessionId, 3, waitOptions);
  assert.match(lastText(await sessions.getAgentSnapshot(session.sessionId)), /declined/);
});

test("aiAgentSteersADurableWaitWithAQueuedMessageThroughRoutes", async () => {
  const session = await routes.startSession();
  await routes.post(session, "messages", { text: "/wait 600 a long pause" });
  await waitForToolStatus(session.sessionId, "waiting_for_timer");
  await routes.post(session, "messages", { text: "first queued" });
  await routes.post(session, "messages", { text: "second queued" });
  const queued = (await routes.getJson<{ queued_messages: { message_id: string }[] }>(session, "snapshot")).queued_messages;
  assert.equal(queued.length, 2);

  assert.equal((await routes.request(session, `queued-messages/${queued[0]!.message_id}`, { method: "DELETE" })).status, 200);
  assert.equal((await routes.request(session, `queued-messages/${queued[0]!.message_id}`, { method: "DELETE" })).status, 409);
  assert.equal((await routes.post(session, "steer", { message_id: queued[1]!.message_id })).status, 200);
  const snapshot = await sessions.waitForAgentSnapshot(
    session.sessionId,
    (current) => current.state?.status === "waiting_for_user_message" && current.state.turnCount === 2,
    "the steered turn",
    waitOptions,
  );
  assert.equal(lastText(snapshot), "Local demo response: second queued");
  assert.equal(messageTexts(snapshot).includes("first queued"), false);
});

test("aiAgentExecutesAnApprovedPlanThroughRoutes", async () => {
  const session = await routes.startSession();
  assert.equal((await routes.post(session, "messages", { text: "summarize the notes", plan_mode: true })).status, 200);
  await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);
  const draft = (await sessions.getAgentSnapshot(session.sessionId)).plan;
  assert.equal(draft?.status, "draft");

  assert.equal((await routes.post(session, "plan/execute", { revision: draft.revision + 1 })).status, 409);
  assert.equal((await routes.post(session, "plan/execute", { revision: draft.revision })).status, 200);
  await sessions.waitForAgentSnapshot(
    session.sessionId,
    (current) => current.plan?.status === "completed" && current.state?.status === "waiting_for_user_message",
    "the executed plan",
    waitOptions,
  );
});

test("aiAgentCompactsALongConversation", async () => {
  const sessionId = await startSession();
  for (let turn = 1; turn <= 12; turn += 1) {
    await sessions.sendUserMessage(sessionId, `message ${turn}: ${"the agent keeps every message in Dex. ".repeat(4)}`);
    await sessions.waitForCompletedTurns(sessionId, turn, waitOptions);
  }
  const snapshot = await sessions.getAgentSnapshot(sessionId);
  assert.ok((snapshot.contextSummary?.lastSequence ?? 0) > 0);
  assert.equal(snapshot.messages.filter((message) => message.role === "user").length, 12);
});

test("aiAgentReconnectsToAnActiveTurnWithItsResumeToken", async () => {
  const session = await routes.startSession();
  await routes.post(session, "messages", { text: "/wait 2 a short pause" });
  await waitForToolStatus(session.sessionId, "waiting_for_timer");
  const stored = await routes.getJson<{ resumeToken: string | null }>(session, "chat");
  assert.notEqual(stored.resumeToken, null);

  const replay = await routes.request(session, "stream", { headers: { "x-agent-resume-token": stored.resumeToken! } });
  const chunks = await replay.text();
  assert.match(chunks, /"toolName":"durable_wait"/);
  assert.match(chunks, /"finishReason":"stop"/);
  const forged = await routes.request(session, "stream", { headers: { "x-agent-resume-token": "forged" } });
  assert.equal(forged.status, 400);
});

test("aiAgentRoutesRejectBadRequests", async () => {
  const session = await routes.startSession();
  const stranger = { ...session, sessionToken: "0".repeat(64) };
  assert.equal((await routes.request(stranger, "snapshot")).status, 403);
  assert.equal((await routes.request({ ...session, sessionToken: "" }, "messages")).status, 403);
  assert.equal((await routes.request(session, "messages", { method: "POST" })).status, 400);
  assert.equal((await routes.post(session, "plan/execute", {})).status, 400);
  assert.equal((await routes.post(session, "steer", { message_id: 7 })).status, 400);
  assert.equal((await routes.request(session, "messages?before_sequence=abc")).status, 400);
  const chat = await fetch(`${routes.baseUrl}/api/chat`, { method: "POST", headers: routes.headers(session), body: "{}" });
  assert.equal(chat.status, 400);

  await client.stopFlow(session.sessionId);
  const closed = await routes.post(session, "messages", { text: "hello" });
  assert.equal(closed.status, 409);
  assert.match(await closed.text(), /closed/);
});

test("aiAgentSessionRunsTheModelItsStartFormPicked", async () => {
  assert.deepEqual(await (await fetch(`${routes.baseUrl}/settings`)).json(), {
    model: "mock/dex",
    models: ["mock/dex", "mock/dex-mini"],
  });
  for (const body of ['{"model":"openai/gpt-5"}', '["mock/dex"]', '"mock/dex"', '{"model":7}']) {
    const refused = await fetch(`${routes.baseUrl}/sessions`, { method: "POST", headers: { "content-type": "application/json" }, body });
    assert.equal(refused.status, 400, body);
  }
  const picked = await routes.startSession({ model: "mock/dex-mini" });
  const defaulted = await routes.startSession({});
  for (const [session, model] of [[picked, "mock/dex-mini"], [defaulted, "mock/dex"]] as const) {
    await routes.post(session, "messages", { text: "/model" });
    await sessions.waitForCompletedTurns(session.sessionId, 1, waitOptions);
    const snapshot = await sessions.getAgentSnapshot(session.sessionId);
    assert.equal(lastText(snapshot), `Local demo response: ${model}`);
    // Only the model name is stored; the Worker owns the client and its key.
    assert.deepEqual(snapshot.sessionSettings, { model });
  }
});

test("aiAgentMcpConfigurationReadsSecretsFromTheEnvironment", () => {
  process.env.AI_AGENT_TEST_TOKEN = "Bearer secret";
  const yaml = "servers:\n  - name: tickets\n    transport: streamable_http\n    url: https://tickets.example/mcp\n" +
    "    headers:\n      Authorization: AI_AGENT_TEST_TOKEN\n";
  assert.deepEqual(loadMcpServers(yaml), [
    {
      transport: "streamable_http",
      name: "tickets",
      url: "https://tickets.example/mcp",
      headers: { Authorization: "Bearer secret" },
      trustsToolAnnotations: false,
    },
  ]);
  assert.throws(() => loadMcpServers(`${yaml}    tools: {}\n`), /unsupported keys tools/);
  assert.throws(() => loadMcpServers(yaml.replace("AI_AGENT_TEST_TOKEN", "AI_AGENT_MISSING")), /AI_AGENT_MISSING/);
});

test("aiAgentStreamFailureEndsOnlyThatResponse", async () => {
  // A Client of its own, closed mid-stream, makes the stream's Dex reads fail.
  const streamClient = new Client(new Registry([flow]), blobCache, { serverAddress });
  const streamRoutes = await AiAgentRoutes.start(streamClient);
  try {
    const session = await streamRoutes.startSession();
    const message = { id: "user-1", role: "user", parts: [{ type: "text", text: "/wait 600 a long pause" }] };
    const response = await fetch(`${streamRoutes.baseUrl}/api/chat`, {
      method: "POST",
      headers: streamRoutes.headers(session),
      body: JSON.stringify({ id: session.sessionId, messages: [message] }),
    });
    await waitForToolStatus(session.sessionId, "waiting_for_timer");
    await streamClient.close();
    await response.text().catch(() => "the stream was cut");

    assert.equal((await routes.request(session, "snapshot")).status, 200);
    assert.equal((await fetch(`${streamRoutes.baseUrl}/`)).status, 200);
  } finally {
    await streamRoutes.close();
  }
});

test("aiAgentRerunsAModelCallCutOffByAWorkerKill", async () => {
  const sessionId = await startSession();
  await sessions.sendUserMessage(sessionId, "/slow 4 after the crash");
  await sessions.waitForAgentState(sessionId, (state) => state.status === "running_model", "a model call", waitOptions);
  await killAndRestartWorker();
  await sessions.waitForCompletedTurns(sessionId, 1, waitOptions);
  assert.equal(lastText(await sessions.getAgentSnapshot(sessionId)), "Local demo response: /slow 4 after the crash");
});

test("aiAgentRetriesAReadToolStepCutOffByAWorkerKill", async () => {
  const sessionId = await startSession();
  await sessions.sendUserMessage(sessionId, '/tool notes__search_notes {"query":"Step","delay_seconds":4}');
  await pollUntil(
    async () => (await sessions.listAgentEvents(sessionId)).find((event) => event.type === "tool_call_started"),
    { description: "a running read tool", ...waitOptions },
  );
  await killAndRestartWorker();
  await sessions.waitForCompletedTurns(sessionId, 1, waitOptions);
  const started = (await sessions.listAgentEvents(sessionId)).filter((event) => event.type === "tool_call_started");
  assert.ok(started.length >= 2);
  assert.match(lastText(await sessions.getAgentSnapshot(sessionId)), /in its own Step/);
});

test("aiAgentFinishesADurableWaitAfterTheWorkerIsKilled", async () => {
  const sessionId = await startSession();
  await sessions.sendUserMessage(sessionId, "/wait 3 a short pause");
  await waitForToolStatus(sessionId, "waiting_for_timer");
  await killAndRestartWorker();
  await sessions.waitForCompletedTurns(sessionId, 1, waitOptions);
  assert.match(lastText(await sessions.getAgentSnapshot(sessionId)), /"status":"completed","waited_seconds":3/);
});

/** One Slack post the Worker's connector mock answered, as the Worker recorded it. */
interface SlackMockCall {
  readonly flowId: string;
  readonly callId: string;
  readonly idempotencyKey?: string;
  readonly input: unknown;
  readonly branch?: string;
  readonly retry?: boolean;
  readonly replay?: boolean;
}

async function readSlackCalls(): Promise<SlackMockCall[]> {
  const text = await readFile(workerEnvironment.AI_AGENT_TEST_SLACK_CALLS_FILE ?? "", "utf8");
  return text
    .split("\n")
    .filter((line) => line !== "")
    .map((line) => JSON.parse(line) as SlackMockCall);
}

function startWorker(): Promise<KillableWorkerProcess> {
  return KillableWorkerProcess.start({
    entryModulePath: fileURLToPath(new URL("./ai-agent-worker-main.js", import.meta.url)),
    serverAddress,
    logPath: join(stateDirectory, `worker-${randomUUID()}.log`),
    environment: workerEnvironment,
  });
}

async function killAndRestartWorker(): Promise<void> {
  await worker.kill();
  worker = await startWorker();
}

async function startSession(): Promise<string> {
  const sessionId = `ai-agent-${randomUUID()}`;
  await sessions.startSession(sessionId);
  return sessionId;
}

function waitForToolStatus(sessionId: string, status: string): Promise<AgentSnapshot> {
  return sessions.waitForAgentSnapshot(
    sessionId,
    (snapshot) => snapshot.toolCalls.some((call) => call.status === status),
    `a tool call in ${status}`,
    waitOptions,
  );
}

function messageTexts(snapshot: AgentSnapshot): string[] {
  return snapshot.messages.flatMap((message) => (message.text === null ? [] : [message.text]));
}

function lastText(snapshot: AgentSnapshot): string {
  return messageTexts(snapshot).at(-1) ?? "";
}

interface RouteSession {
  readonly sessionId: string;
  readonly sessionToken: string;
}

/** The example's router on a free port, driven the way its page drives it. */
class AiAgentRoutes {
  private constructor(
    private readonly server: Server,
    public readonly baseUrl: string,
  ) {}

  public static async start(routeClient: Client): Promise<AiAgentRoutes> {
    const app = express().use(express.json()).use("/ai-agent", createAiAgentRouter(routeClient, flow, sessionKey, loadAiAgentConfig().models));
    const server = await new Promise<Server>((resolve) => {
      const listening = app.listen(0, "127.0.0.1", () => resolve(listening));
    });
    return new AiAgentRoutes(server, `http://127.0.0.1:${(server.address() as AddressInfo).port}/ai-agent`);
  }

  public async startSession(body: unknown = {}): Promise<RouteSession> {
    const init = { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
    const started = (await (await fetch(`${this.baseUrl}/sessions`, init)).json()) as {
      session_id: string;
      session_token: string;
    };
    return { sessionId: started.session_id, sessionToken: started.session_token };
  }

  public headers(session: RouteSession): Record<string, string> {
    return { "content-type": "application/json", [sessionTokenHeaderName]: session.sessionToken };
  }

  /** `path` is under `/sessions/:id/`, except `chat` and `stream`, which name the useChat routes. */
  public request(session: RouteSession, path: string, init: RequestInit = {}): Promise<Response> {
    const url =
      path === "chat" ? `/api/chat/${session.sessionId}`
      : path === "stream" ? `/api/chat/${session.sessionId}/stream`
      : `/sessions/${session.sessionId}/${path}`;
    return fetch(`${this.baseUrl}${url}`, { ...init, headers: { ...this.headers(session), ...(init.headers as object) } });
  }

  public post(session: RouteSession, path: string, body: unknown): Promise<Response> {
    return this.request(session, path, { method: "POST", body: JSON.stringify(body) });
  }

  public async getJson<T>(session: RouteSession, path: string): Promise<T> {
    const response = await this.request(session, path);
    assert.equal(response.status, 200, await response.clone().text());
    return (await response.json()) as T;
  }

  public async postChat(session: RouteSession, messages: unknown[]): Promise<string> {
    const response = await fetch(`${this.baseUrl}/api/chat`, {
      method: "POST",
      headers: this.headers(session),
      body: JSON.stringify({ id: session.sessionId, messages }),
    });
    assert.equal(response.status, 200);
    return response.text();
  }

  public waitForQuestion(session: RouteSession): Promise<{ interrupt_id: string; choices: string[] }> {
    return pollUntil(async () => {
      const snapshot = await this.getJson<{ pending_interrupts: { interrupt_id: string; kind: string; choices: string[] }[] }>(
        session,
        "snapshot",
      );
      return snapshot.pending_interrupts.find((interrupt) => interrupt.kind === "question");
    }, { description: "a pending question", ...waitOptions });
  }

  public async close(): Promise<void> {
    this.server.closeAllConnections();
    await new Promise((resolve) => this.server.close(resolve));
  }
}
