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

import { Router, type NextFunction, type Request, type Response } from "express";

import { RpcLockConflictError, type Client } from "@superdurable/dex";
import { startAgentSession, type AgentSessionFlow } from "@superdurable/dex-agents";
import { convertAgentSnapshotToJson } from "@superdurable/dex-agents/ui-stream";

import { DAY_MS } from "../../../config/env.js";
import { isFlowMissingOrInactive } from "../../../service-errors.js";

/** Header that carries the session token returned by `POST /sessions`. */
export const orderSupportSessionTokenHeaderName = "X-Agent-Session-Token";

class HttpError extends Error {
  public constructor(public readonly status: number, message: string) {
    super(message);
  }
}

type JsonBody = Record<string, unknown>;

/**
 * Routes of one order-support session Flow, the same for every harness. The session ID is the Flow ID.
 * Each session route checks the session token, an HMAC of the session ID.
 */
export function createOrderSupportRouter(client: Client, flow: AgentSessionFlow, sessionKey: string): Router {
  const router = Router();
  const issueSessionToken = (sessionId: string) => createHmac("sha256", sessionKey).update(sessionId).digest("hex");
  const sessionIdOf = (request: Request): string => {
    const sessionId = String(request.params.id);
    const expected = Buffer.from(issueSessionToken(sessionId));
    const presented = Buffer.from(request.header(orderSupportSessionTokenHeaderName) ?? "");
    if (presented.length !== expected.length || !timingSafeEqual(presented, expected)) {
      throw new HttpError(403, "this caller has no token for the order-support session");
    }
    return sessionId;
  };

  router.post("/sessions", async (request, response) => {
    const initialMessage = optionalString(requireBody(request), "message");
    // The server picks the session ID, so a caller cannot name another session.
    const sessionId = `order-support-${randomUUID()}`;
    await startAgentSession(client, flow, sessionId, { initialMessage }, { timeoutMs: 30 * DAY_MS });
    response.json({ session_id: sessionId, session_token: issueSessionToken(sessionId) });
  });
  router.get("/sessions/:id/snapshot", async (request, response) => {
    response.json(convertAgentSnapshotToJson(await client.invokeRPC(flow.getAgentSnapshot, sessionIdOf(request))));
  });
  router.post("/sessions/:id/messages", async (request, response) => {
    const sessionId = sessionIdOf(request);
    const message = { text: requireString(requireBody(request), "text") };
    sendAccepted(response, await client.invokeRPC(flow.sendUserMessage, sessionId, message));
  });
  router.post("/sessions/:id/interrupts/:interruptId/approve", async (request, response) => {
    const sessionId = sessionIdOf(request);
    const approval = { interruptId: String(request.params.interruptId), approved: true, rejectionMessage: null };
    sendAccepted(response, await client.invokeRPC(flow.resolveInterrupt, sessionId, approval));
  });
  router.post("/sessions/:id/interrupts/:interruptId/reject", async (request, response) => {
    const sessionId = sessionIdOf(request);
    const rejectionMessage = optionalString(requireBody(request), "reason");
    const rejection = { interruptId: String(request.params.interruptId), approved: false, rejectionMessage };
    sendAccepted(response, await client.invokeRPC(flow.resolveInterrupt, sessionId, rejection));
  });

  router.use(sendErrorResponse);
  return router;
}

function sendErrorResponse(error: unknown, _request: Request, response: Response, _next: NextFunction): void {
  const [status, message] = describeError(error);
  if (status >= 500) {
    console.error("order-support route failed", error);
  }
  response.status(status).type("text/plain").send(message);
}

function describeError(error: unknown): [number, string] {
  if (error instanceof HttpError) return [error.status, error.message];
  if (isFlowMissingOrInactive(error)) return [409, "the order-support session is closed or no longer retained"];
  if (error instanceof RpcLockConflictError) return [409, "the order-support session is busy; try again"];
  // The JSON body parser reports a malformed body with a 4xx status.
  const status = (error as { status?: unknown } | null)?.status;
  if (typeof status === "number" && status >= 400 && status < 500) return [status, "the request body must be JSON"];
  return [500, "the order-support request failed"];
}

/** Returns the JSON object body; a missing body counts as an empty object. */
function requireBody(request: Request): JsonBody {
  const body: unknown = request.body ?? {};
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
