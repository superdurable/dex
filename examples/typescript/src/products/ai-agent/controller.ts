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

import { createHmac, randomUUID, timingSafeEqual } from "node:crypto";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import type { ReadableStream as NodeReadableStream } from "node:stream/web";
import { fileURLToPath } from "node:url";

import express, { Router, type NextFunction, type Request, type Response } from "express";

import { ChannelMessageNotFoundError, RpcLockConflictError, type Client } from "@superdurable/dex";
import {
  agentResumeTokenHeaderName,
  convertAgentSnapshotToJson,
  createAgentUIMessageStreamResponse,
  readAgentSessionUIMessages,
  readAgentToolApprovalResponses,
  resolveAgentToolApprovalsWithUIMessageStream,
  sendAgentUserMessageWithUIMessageStream,
} from "@superdurable/dex-agents/ui-stream";

import { DAY_MS } from "../../config/env.js";
import { isFlowMissingOrInactive } from "../../service-errors.js";
import type { AiAgentFlow } from "./ai-agent-flow.js";

/** Header that carries the session token returned by `POST /sessions`. */
export const sessionTokenHeaderName = "X-Agent-Session-Token";

class HttpError extends Error {
  public constructor(public readonly status: number, message: string) {
    super(message);
  }
}

type JsonBody = Record<string, unknown>;

/**
 * Routes of the AI Agent. The session ID is the Flow ID. Every session route first checks the session
 * token, an HMAC of the session ID, so only the browser that started a session can use it. A session
 * picks one of `models`, the Worker's allowed models, when it starts; the first is the default.
 */
export function createAiAgentRouter(client: Client, flow: AiAgentFlow, sessionKey: string, models: readonly string[]): Router {
  const router = Router();
  const session = { client, flow };
  const issueSessionToken = (sessionId: string) => createHmac("sha256", sessionKey).update(sessionId).digest("hex");
  const authorizeSession = (request: Request, sessionId: string): string => {
    const expected = Buffer.from(issueSessionToken(sessionId));
    const presented = Buffer.from(request.header(sessionTokenHeaderName) ?? "");
    if (presented.length !== expected.length || !timingSafeEqual(presented, expected)) {
      throw new HttpError(403, "this browser has no token for the AI Agent session");
    }
    return sessionId;
  };
  const sessionIdOf = (request: Request) => authorizeSession(request, String(request.params.id));
  router.use("/", express.static(fileURLToPath(new URL("./ui/", import.meta.url))));

  router.get("/settings", (_request, response) => {
    response.json({ model: models[0], models });
  });
  router.post("/sessions", async (request, response) => {
    const model = requireBody(request).model ?? models[0];
    if (typeof model !== "string" || !models.includes(model)) {
      throw new HttpError(400, "model must be one of the models in /settings");
    }
    // The server picks the session ID, and settings carry only the model name the Worker resolves.
    const sessionId = `ai-agent-${randomUUID()}`;
    const start = { initialMessage: null, sessionSettings: { model } };
    await client.startFlow(flow, sessionId, start, { timeoutMs: 30 * DAY_MS });
    response.json({ session_id: sessionId, session_token: issueSessionToken(sessionId) });
  });

  // The useChat routes: stored messages, a new message or approval answers, and reconnects.
  router.get("/api/chat/:id", async (request, response) => {
    response.json(await readAgentSessionUIMessages({ ...session, flowId: sessionIdOf(request) }));
  });
  router.post("/api/chat", async (request, response) => {
    const body = requireBody(request);
    const flowId = authorizeSession(request, requireString(body, "id"));
    const lastMessage = Array.isArray(body.messages) ? (body.messages.at(-1) as JsonBody | undefined) : undefined;
    const signal = abortSignalOf(response);
    if (lastMessage?.role === "assistant") {
      const approvals = readAgentToolApprovalResponses(lastMessage);
      await sendFetchResponse(response, resolveAgentToolApprovalsWithUIMessageStream({ ...session, flowId, approvals, signal }));
      return;
    }
    const parts = (Array.isArray(lastMessage?.parts) ? lastMessage.parts : []) as JsonBody[];
    const text = parts.map((part) => (part.type === "text" ? String(part.text) : "")).join("");
    await sendFetchResponse(response, sendAgentUserMessageWithUIMessageStream({ ...session, flowId, text, signal }));
  });
  router.get("/api/chat/:id/stream", async (request, response) => {
    const resumeToken = request.header(agentResumeTokenHeaderName);
    const options = { ...session, flowId: sessionIdOf(request), resumeToken, signal: abortSignalOf(response) };
    await sendFetchResponse(response, createAgentUIMessageStreamResponse(options));
  });

  // Snapshot and history.
  router.get("/sessions/:id/snapshot", async (request, response) => {
    response.json(convertAgentSnapshotToJson(await client.invokeRPC(flow.getAgentSnapshot, sessionIdOf(request))));
  });
  router.get("/sessions/:id/messages", async (request, response) => {
    const beforeSequenceText = String(request.query.before_sequence ?? "");
    if (beforeSequenceText !== "" && !/^\d+$/.test(beforeSequenceText)) {
      throw new HttpError(400, "before_sequence must be a message sequence");
    }
    const beforeSequence = beforeSequenceText === "" ? null : Number(beforeSequenceText);
    const page = await client.invokeRPC(flow.listAgentMessages, sessionIdOf(request), { beforeSequence, limit: 100 });
    // Only the session's Steps read the framework item, which can hold provider replay data.
    const messages = page.messages.map((message) => ({
      sequence: message.sequence,
      turn_id: message.turnId,
      role: message.role,
      text: message.text,
      tool_call_id: message.toolCallId,
      tool_name: message.toolName,
      tool_arguments: message.toolArguments,
      tool_output: message.toolOutput,
      created_at: message.createdAt,
    }));
    response.json({ messages, next_before_sequence: page.nextBeforeSequence });
  });

  // Queue, steer, delete, and cancel.
  router.post("/sessions/:id/messages", async (request, response) => {
    const body = requireBody(request);
    const message = { text: requireString(body, "text"), planMode: body.plan_mode === true };
    sendAccepted(response, await client.invokeRPC(flow.sendUserMessage, sessionIdOf(request), message));
  });
  router.post("/sessions/:id/steer", async (request, response) => {
    const body = requireBody(request);
    const steer = { messageId: optionalString(body, "message_id"), text: optionalString(body, "text") };
    sendAccepted(response, await client.invokeRPC(flow.steerUserMessage, sessionIdOf(request), steer));
  });
  router.delete("/sessions/:id/queued-messages/:messageId", async (request, response) => {
    const queued = { messageId: request.params.messageId };
    sendAccepted(response, await client.invokeRPC(flow.deleteQueuedMessage, sessionIdOf(request), queued));
  });
  router.post("/sessions/:id/cancel", async (request, response) => {
    sendAccepted(response, await client.invokeRPC(flow.cancelTurn, sessionIdOf(request), { turnId: null }));
  });

  // Questions from ask_user, and plan execution.
  router.post("/sessions/:id/questions/:interruptId/answer", async (request, response) => {
    const answer = { interruptId: request.params.interruptId, answer: requireString(requireBody(request), "answer") };
    sendAccepted(response, await client.invokeRPC(flow.answerQuestion, sessionIdOf(request), answer));
  });
  router.post("/sessions/:id/questions/:interruptId/decline", async (request, response) => {
    const decline = { interruptId: request.params.interruptId, approved: false, rejectionMessage: null };
    sendAccepted(response, await client.invokeRPC(flow.resolveInterrupt, sessionIdOf(request), decline));
  });
  router.post("/sessions/:id/plan/execute", async (request, response) => {
    const revision = requireBody(request).revision;
    if (!Number.isInteger(revision)) {
      throw new HttpError(400, "revision must be an integer");
    }
    sendAccepted(response, await client.invokeRPC(flow.executePlan, sessionIdOf(request), { revision: revision as number }));
  });

  router.use(sendErrorResponse);
  return router;
}

function sendErrorResponse(error: unknown, _request: Request, response: Response, _next: NextFunction): void {
  const [status, message] = describeError(error);
  if (status >= 500) {
    console.error("AI Agent route failed", error);
  }
  if (response.headersSent) {
    response.destroy();
    return;
  }
  response.status(status).type("text/plain").send(message);
}

function describeError(error: unknown): [number, string] {
  if (error instanceof HttpError) return [error.status, error.message];
  if (isFlowMissingOrInactive(error)) return [409, "the AI Agent session is closed or no longer retained; start a new one"];
  if (error instanceof RpcLockConflictError) return [409, "the AI Agent session is busy; try again"];
  if (error instanceof ChannelMessageNotFoundError) return [409, "the queued message was consumed; reload the snapshot"];
  // The JSON body parser reports a malformed body with a 4xx status.
  const status = (error as { status?: unknown } | null)?.status;
  if (typeof status === "number" && status >= 400 && status < 500) return [status, "the request body must be JSON"];
  return [500, "the AI Agent request failed"];
}

function requireBody(request: Request): JsonBody {
  const body: unknown = request.body;
  if (typeof body !== "object" || body === null || Array.isArray(body)) {
    throw new HttpError(400, "the request body must be a JSON object");
  }
  return body as JsonBody;
}

function requireString(body: JsonBody, name: string): string {
  const value = body[name];
  if (typeof value !== "string" || value.trim() === "") {
    throw new HttpError(400, `${name} must be a non-empty string`);
  }
  return value;
}

function optionalString(body: JsonBody, name: string): string | null {
  return body[name] === undefined || body[name] === null ? null : requireString(body, name);
}

/** Answers 200 when the session accepted the request, and 409 when its state no longer allows it. */
function sendAccepted(response: Response, isAccepted: boolean): void {
  response.status(isAccepted ? 200 : 409).json({ accepted: isAccepted });
}

function abortSignalOf(response: Response): AbortSignal {
  const abortController = new AbortController();
  response.on("close", () => abortController.abort());
  return abortController.signal;
}

// A Dex error mid-stream must end only this response, never the process.
async function sendFetchResponse(response: Response, pendingFetchResponse: Promise<globalThis.Response>): Promise<void> {
  const fetchResponse = await pendingFetchResponse;
  response.status(fetchResponse.status).setHeaders(fetchResponse.headers);
  if (fetchResponse.body === null) {
    response.end();
    return;
  }
  try {
    await pipeline(Readable.fromWeb(fetchResponse.body as NodeReadableStream), response);
  } catch (error) {
    // The browser reconnects with its resume token; a closed tab is not worth a warning.
    if ((error as { code?: unknown }).code !== "ERR_STREAM_PREMATURE_CLOSE") {
      console.warn("AI Agent UI stream ended early", error);
    }
  }
}
