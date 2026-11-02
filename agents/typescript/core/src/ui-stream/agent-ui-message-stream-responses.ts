// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

import { RpcLockConflictError, type Client } from "@superdurable/dex";

import type { AgentSessionFlow } from "../agent-session-flow.js";
import {
  describeToolCallOutcome,
  isFinalToolCallStatus,
  type AgentToolCall,
} from "../agent-session-values.js";
import {
  findAgentEventResumeTokenBeforeTurn,
  parseTurnNumber,
  probeAgentEventResumeToken,
  readAgentEventsTailResumeToken,
  requireAgentEventPollTimeoutMs,
} from "./agent-event-stream-positions.js";
import { AgentSessionRecordReader, type StoredToolResult } from "./agent-session-record-reader.js";
import {
  prepareAgentUIStreamStart,
  readAgentUIMessageChunkBatches,
  type AgentUIMessageStreamOptions,
  type AgentUIStreamStart,
} from "./agent-ui-message-stream.js";
import {
  agentResumeTokenDataPartType,
  agentUIMessageStreamHeaders,
  type AgentUIMessageChunk,
} from "./agent-ui-message-types.js";
import {
  createAgentUIStreamCursor,
  decodeAgentUIStreamCursor,
  encodeAgentUIStreamCursor,
  toolCallUIStateOfRecord,
  type AgentUIStreamCursor,
} from "./agent-ui-stream-cursor.js";
import {
  createServerSentEventResponse,
  mergeHeaders,
  serverSentEventKeepAliveFrame,
} from "./server-sent-event-responses.js";

const lockConflictRetryTimeoutMs = 5_000;
const lockConflictRetryIntervalMs = 50;
const maximumApprovalsPerRequest = 100;
const maximumToolCallIdLength = 512;
const staleApprovalText = "The approval is no longer pending.";
const assistantMessageIdPattern = /^(turn-[0-9]+)-assistant$/;

/**
 * Request header name for a UI stream resume token.
 *
 * Send the token in this header rather than the URL: a token can reach 64 KiB, beyond many URL limits.
 */
export const agentResumeTokenHeaderName = "x-agent-resume-token";

/** {@link AgentUIMessageStreamOptions} plus headers to add to the Response. */
export interface AgentUIMessageStreamResponseOptions extends AgentUIMessageStreamOptions {
  /** Extra response headers; they override the protocol headers with the same name. */
  readonly headers?: Readonly<Record<string, string>>;
}

/** Options of {@link sendAgentUserMessageWithUIMessageStream}. */
export interface AgentUserMessageUIStreamOptions
  extends Omit<AgentUIMessageStreamResponseOptions, "resumeToken"> {
  /** Text of the user message. */
  readonly text: string;
}

/**
 * One tool approval decision, the content of an AI SDK `tool-approval-response`.
 *
 * `useChat` records it on a tool part when the application calls `addToolApprovalResponse`.
 */
export interface AgentToolApprovalResponse {
  /** Approval ID from the `tool-approval-request` part: the `interrupt_id` of the pending interrupt. */
  readonly approvalId: string;
  /** Whether the tool call may run. */
  readonly approved: boolean;
  /** Message the model sees after a rejection. Ignored on approval. */
  readonly reason?: string | undefined;
  /**
   * Turn whose assistant message holds the decision, such as `"turn-3"`, or `undefined` when unknown.
   * {@link resolveAgentToolApprovalsWithUIMessageStream} treats a decision bound to another turn than the
   * active one as stale, so a stale browser tab cannot answer a later turn's call that reuses a tool call
   * ID. {@link readAgentToolApprovalResponses} sets it from the message ID.
   */
  readonly turnId?: string | undefined;
}

/** Options of {@link resolveAgentToolApproval}. */
export interface AgentToolApprovalOptions {
  /** Client used for the RPC. */
  readonly client: Client;
  /** The agent session Flow. */
  readonly flow: AgentSessionFlow;
  /** Session ID: the Flow ID of the agent session. */
  readonly flowId: string;
  /** The decision to send. */
  readonly approval: AgentToolApprovalResponse;
}

/** Options of {@link resolveAgentToolApprovalsWithUIMessageStream}. */
export interface AgentToolApprovalsUIStreamOptions extends AgentUIMessageStreamResponseOptions {
  /**
   * Decisions from the browser, such as the result of {@link readAgentToolApprovalResponses}. They are
   * untrusted: only the first decision per approval ID counts, at most 100 are read, and only interrupts
   * pending in the session's active turn are resolved, by decisions bound to that turn or to none.
   */
  readonly approvals: readonly AgentToolApprovalResponse[];
}

/**
 * Returns a streaming AI SDK UI message stream Response for one agent turn, or 204 when there is none.
 *
 * Mount it as the reconnect endpoint of `useChat`, `GET <api>/<chatId>/stream` by default, with the chat ID
 * as the session ID; the route must check that the caller may read that session. It writes
 * {@link streamAgentUIMessageChunks} as Server-Sent Events with the protocol headers, a keep-alive
 * comment after each empty long poll, and `data: [DONE]` at the end. It stops reading when the browser
 * closes the response.
 *
 * Use the default `DefaultChatTransport`, whose resumed streams count as replays. A stream started with a
 * token replays the token's turn, and one started without a token replays the active turn; either way
 * its `start` part makes the browser reset the message and rebuild it, whatever it held. With
 * `continueAfterResumeToken`, a stream continues exactly after the last part the browser received and
 * sends no `start` part; use it only while the browser still holds that message. Before it answers 200,
 * the helper reads once to check the token's Dex position; a position Dex rejects restarts the turn from
 * its first retained event.
 *
 * @example
 * ```ts
 * // Server: GET /api/chat/:id/stream
 * export async function GET(request: Request, sessionId: string): Promise<Response> {
 *   await authorizeSession(request, sessionId);
 *   return createAgentUIMessageStreamResponse({
 *     client, flow, flowId: sessionId,
 *     resumeToken: request.headers.get(agentResumeTokenHeaderName) ?? undefined,
 *     signal: request.signal,
 *   });
 * }
 *
 * // Browser
 * let resumeToken: string | undefined = initial.resumeToken ?? undefined;
 * const chat = useChat({
 *   id: sessionId,
 *   messages: initial.messages,
 *   resume: resumeToken !== undefined,
 *   onData: (part) => {
 *     if (part.type === "data-agent-resume-token") resumeToken = part.data as string;
 *   },
 *   transport: new DefaultChatTransport({
 *     api: "/api/chat",
 *     prepareReconnectToStreamRequest: ({ id }) => ({
 *       api: `/api/chat/${id}/stream`,
 *       headers: resumeToken === undefined ? {} : { "x-agent-resume-token": resumeToken },
 *     }),
 *   }),
 *   sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses,
 * });
 * ```
 * @param options - Session, start position, polling, and extra headers.
 * @returns 200 with the stream; 204 when the token's turn already finished, or when the turn to replay is
 * not active or not on the Stream; 400 with a text body when `resumeToken` is invalid.
 * @throws {RangeError} When `pollTimeoutMs` is not a positive whole number of seconds.
 * @throws {DexServiceError} When reading the start position fails.
 */
export async function createAgentUIMessageStreamResponse(
  options: AgentUIMessageStreamResponseOptions,
): Promise<Response> {
  const pollTimeoutMs = requireAgentEventPollTimeoutMs(options.pollTimeoutMs);
  if (options.resumeToken !== undefined && decodeResumeTokenOrUndefined(options.resumeToken) === undefined) {
    return createInvalidResumeTokenResponse(options.headers);
  }
  const start = await prepareAgentUIStreamStart(options);
  if (start === undefined) {
    return new Response(null, { status: 204, headers: mergeHeaders({}, options.headers) });
  }
  return createAgentUIMessageSseResponse(options, start, pollTimeoutMs, []);
}

/**
 * Queues a user message and returns the AI SDK UI message stream Response of the turn it starts.
 *
 * Mount it as the send endpoint of `useChat`, `POST <api>` by default, when the last message in the
 * request body is a user message. The chat ID in the body comes from the browser, so the route must check
 * that the caller may write to that session. The helper reads the session's turn count and the Stream
 * position first and then calls `send_user_message`, so the stream follows the next turn that starts
 * after the message was queued. When an earlier turn is still running, the stream waits for it to end.
 * When other messages were queued before this one, the stream follows the turn of the oldest of them.
 *
 * The session's committed state ends the stream even when no event of the turn reaches the Stream, such
 * as with the Server's Stream Store disabled: once that turn ended, the stream finishes with reason
 * `other`, and the application reloads the stored messages. The RPC needs a running Worker.
 *
 * @example
 * ```ts
 * // POST /api/chat
 * export async function POST(request: Request): Promise<Response> {
 *   const { id, messages } = await request.json();
 *   await authorizeSession(request, id);
 *   const lastMessage = messages.at(-1);
 *   if (lastMessage.role === "assistant") {
 *     return resolveAgentToolApprovalsWithUIMessageStream({
 *       client, flow, flowId: id, approvals: readAgentToolApprovalResponses(lastMessage) });
 *   }
 *   const text = lastMessage.parts.filter((part) => part.type === "text").map((part) => part.text).join("");
 *   return sendAgentUserMessageWithUIMessageStream({ client, flow, flowId: id, text, signal: request.signal });
 * }
 * ```
 * @param options - Session, message text, polling, and extra headers.
 * @returns 200 with the stream; 400 with a text body when the text is blank.
 * @throws {RangeError} When `pollTimeoutMs` is not a positive whole number of seconds.
 * @throws {FlowNotActiveOrNotFoundError} When the session is not running.
 * @throws {DexServiceError} When a read or the RPC fails.
 */
export async function sendAgentUserMessageWithUIMessageStream(
  options: AgentUserMessageUIStreamOptions,
): Promise<Response> {
  const pollTimeoutMs = requireAgentEventPollTimeoutMs(options.pollTimeoutMs);
  const { client, flow, flowId } = options;
  const [state] = await client.getAttributes(flowId, flow.agentState);
  const eventResumeToken = await readAgentEventsTailResumeToken(client, flow, flowId);
  const isQueued = await client.invokeRPC(flow.sendUserMessage, flowId, { text: options.text });
  if (!isQueued) {
    return new Response("the user message text is blank", {
      status: 400,
      headers: mergeHeaders({ "content-type": "text/plain; charset=utf-8" }, options.headers),
    });
  }
  const cursor = createAgentUIStreamCursor(eventResumeToken, null, false);
  // The message's turn starts after every turn that already started.
  const activeTurnNumber = state?.activeTurnId == null ? 0 : (parseTurnNumber(state.activeTurnId) ?? 0);
  cursor.afterTurnNumber = Math.max(state?.turnCount ?? 0, activeTurnNumber);
  return createAgentUIMessageSseResponse(options, { cursor, firstMessage: undefined }, pollTimeoutMs, []);
}

/**
 * Sends one tool approval decision to the session's `resolve_interrupt` RPC.
 *
 * Call it with each AI SDK `tool-approval-response`: its `approvalId` is the `interrupt_id`, and its
 * `reason` becomes the rejection message the model sees. It retries `RpcLockConflictError`, which a call
 * that races the approval Step can raise, for up to 5 seconds. It resolves any pending interrupt of the
 * session, so the route must check that the caller may decide for that session. The RPC needs a running
 * Worker.
 *
 * @example
 * ```ts
 * const isResolved = await resolveAgentToolApproval({
 *   client, flow, flowId: sessionId,
 *   approval: { approvalId: "call_42", approved: false, reason: "Use the sandbox account" },
 * });
 * ```
 * @param options - Session and decision.
 * @returns `true` when the interrupt was pending and is now resolved; `false` when it is unknown, already
 * resolved, or past its deadline.
 * @throws {RpcLockConflictError} When the lock stays busy for 5 seconds.
 * @throws {FlowNotActiveOrNotFoundError} When the session is not running.
 */
export async function resolveAgentToolApproval(options: AgentToolApprovalOptions): Promise<boolean> {
  const { approval } = options;
  const deadline = Date.now() + lockConflictRetryTimeoutMs;
  for (;;) {
    try {
      return await options.client.invokeRPC(options.flow.resolveInterrupt, options.flowId, {
        interruptId: approval.approvalId,
        approved: approval.approved,
        rejectionMessage: approval.approved ? null : (approval.reason ?? null),
      });
    } catch (error: unknown) {
      if (!(error instanceof RpcLockConflictError) || Date.now() >= deadline) {
        throw error;
      }
    }
    await delay(lockConflictRetryIntervalMs);
  }
}

/**
 * Returns the tool approval decisions recorded on an AI SDK assistant message.
 *
 * `useChat` sets a tool part to `approval-responded` when the application calls
 * `addToolApprovalResponse`, then sends the message list with `sendAutomaticallyWhen`. Pass the last
 * message of that request body. The message comes from the browser, so every field is checked; parts
 * that are not well-formed responded approvals are skipped.
 *
 * Each decision is bound to the message's turn when the message ID is `<turnId>-assistant`, as the agent UI
 * streams and snapshots name assistant messages; a message with another ID gives unbound decisions.
 *
 * @param message - An AI SDK `UIMessage`, typically the last message of a `useChat` request body.
 * @returns One decision per `approval-responded` tool part, in part order; empty for anything else.
 */
export function readAgentToolApprovalResponses(message: unknown): AgentToolApprovalResponse[] {
  if (!isRecord(message) || !Array.isArray(message["parts"])) {
    return [];
  }
  const messageId = message["id"];
  const turnId = typeof messageId === "string" ? assistantMessageIdPattern.exec(messageId)?.[1] : undefined;
  const approvals: AgentToolApprovalResponse[] = [];
  for (const part of message["parts"] as unknown[]) {
    if (!isRecord(part) || part["state"] !== "approval-responded" || !isToolPartType(part["type"])) {
      continue;
    }
    const approval = part["approval"];
    if (!isRecord(approval) || typeof approval["id"] !== "string" || typeof approval["approved"] !== "boolean") {
      continue;
    }
    const reason = approval["reason"];
    approvals.push({
      approvalId: approval["id"],
      approved: approval["approved"],
      ...(typeof reason === "string" ? { reason } : {}),
      ...(turnId === undefined ? {} : { turnId }),
    });
  }
  return approvals;
}

/**
 * Sends tool approval decisions and returns the AI SDK UI message stream Response that continues the turn.
 *
 * Mount it in the send endpoint of `useChat` for requests whose last message is an assistant message,
 * which `sendAutomaticallyWhen: lastAssistantMessageIsCompleteWithApprovalResponses` sends after the
 * application answered every approval. The decisions and the chat ID come from the browser, so the route
 * must check that the caller may decide for that session. Only interrupts that are pending in the
 * session's active turn are resolved.
 *
 * The stream continues the same assistant message. A decision that is no longer pending, because another
 * client or a deadline settled the call or its turn ended, gets the call's known outcome, or an error
 * part when none is known, so the browser leaves `approval-responded` and does not send the decision
 * again. Decisions bound to another turn than the active one, such as from a stale browser tab, are all
 * stale, and the stream then ends after their outcomes. With `resumeToken`, the latest `data-agent-resume-token`, the stream continues exactly after the
 * paused stream. Without one it reads the Stream position and the active turn's tool calls before
 * sending the decisions. The RPCs need a running Worker.
 *
 * @example
 * ```ts
 * return resolveAgentToolApprovalsWithUIMessageStream({
 *   client, flow, flowId: id, approvals: readAgentToolApprovalResponses(messages.at(-1)) });
 * ```
 * @param options - Session, decisions, optional resume token, polling, and extra headers.
 * @returns 200 with the stream, which holds only the stale decisions' outcomes and a `finish` part when
 * no turn is active; 400 with a text body when `resumeToken` is invalid.
 * @throws {RangeError} When `pollTimeoutMs` is not a positive whole number of seconds.
 * @throws {RpcLockConflictError} When a decision's lock stays busy for 5 seconds.
 * @throws {DexServiceError} When a read or an RPC fails.
 */
export async function resolveAgentToolApprovalsWithUIMessageStream(
  options: AgentToolApprovalsUIStreamOptions,
): Promise<Response> {
  const pollTimeoutMs = requireAgentEventPollTimeoutMs(options.pollTimeoutMs);
  const { client, flow, flowId } = options;
  let clientCursor: AgentUIStreamCursor | undefined;
  if (options.resumeToken !== undefined) {
    clientCursor = decodeResumeTokenOrUndefined(options.resumeToken);
    if (clientCursor === undefined) {
      return createInvalidResumeTokenResponse(options.headers);
    }
  }
  // Position before snapshot: events may repeat the snapshot, but none goes missing.
  const tailResumeToken = await readAgentEventsTailResumeToken(client, flow, flowId);
  const snapshot = await client.invokeRPC(flow.getAgentSnapshot, flowId);
  const activeTurnId = snapshot.state?.activeTurnId ?? null;
  // Decisions from an older turn's message settle that message only; the active turn goes elsewhere.
  const isActiveTurnRequest = options.approvals.every(
    (approval) => approval.turnId === undefined || approval.turnId === activeTurnId,
  );
  let start: AgentUIStreamStart | undefined;
  if (!isActiveTurnRequest) {
    const finishedCursor = createAgentUIStreamCursor(tailResumeToken, null, true);
    finishedCursor.isTurnFinished = true;
    start = { cursor: finishedCursor, firstMessage: undefined };
  } else if (clientCursor !== undefined && clientCursor.turnId === activeTurnId && activeTurnId !== null) {
    // The browser keeps its message here, so a rejected position falls back to the tail, never a replay.
    const probe = await probeAgentEventResumeToken(client, flow, flowId, clientCursor.eventResumeToken);
    if (probe.isAccepted) {
      start = { cursor: clientCursor, firstMessage: probe.firstMessage };
    }
  }
  start ??= {
    cursor: await createActiveTurnContinuationCursor(
      client,
      flow,
      flowId,
      tailResumeToken,
      activeTurnId,
      snapshot.toolCalls,
    ),
    firstMessage: undefined,
  };
  const cursor = start.cursor;
  const pendingInterruptIds = new Set(
    snapshot.pendingInterrupts
      .filter((interrupt) =>
        snapshot.toolCalls.some(
          (toolCall) =>
            toolCall.toolCallId === interrupt.toolCallId &&
            toolCall.turnId === activeTurnId &&
            toolCall.status === "awaiting_approval",
        ),
      )
      .map((interrupt) => interrupt.interruptId),
  );
  const staleApprovals: AgentToolApprovalResponse[] = [];
  for (const approval of selectUniqueApprovals(options.approvals)) {
    const isResolved =
      isActiveTurnRequest &&
      pendingInterruptIds.has(approval.approvalId) &&
      (await resolveAgentToolApproval({ client, flow, flowId, approval }));
    if (isResolved) {
      // The interrupt_resolved event follows; until then the call must not count as waiting.
      cursor.toolCalls.set(approval.approvalId, "approval_resolved");
    } else {
      staleApprovals.push(approval);
    }
  }
  const staleOutcomeChunks = await readStaleApprovalOutcomes(
    new AgentSessionRecordReader(client, flow, flowId),
    activeTurnId,
    snapshot.toolCalls,
    staleApprovals,
    cursor,
  );
  return createAgentUIMessageSseResponse(options, start, pollTimeoutMs, staleOutcomeChunks);
}

/** Keeps the first well-formed decision per approval ID, up to the per-request limit. */
function selectUniqueApprovals(approvals: readonly AgentToolApprovalResponse[]): AgentToolApprovalResponse[] {
  const selected = new Map<string, AgentToolApprovalResponse>();
  for (const approval of approvals.slice(0, maximumApprovalsPerRequest)) {
    const approvalId = approval.approvalId;
    if (
      typeof approvalId !== "string" ||
      approvalId === "" ||
      approvalId.length > maximumToolCallIdLength ||
      approvalId.includes("/") ||
      typeof approval.approved !== "boolean" ||
      selected.has(approvalId)
    ) {
      continue;
    }
    selected.set(approvalId, approval);
  }
  return [...selected.values()];
}

/**
 * Returns the parts that settle the decisions `resolve_interrupt` did not accept, and records them in the
 * cursor. A call still open in the active turn gets none: its own events follow.
 */
async function readStaleApprovalOutcomes(
  records: AgentSessionRecordReader,
  activeTurnId: string | null,
  activeTurnToolCalls: readonly AgentToolCall[],
  staleApprovals: readonly AgentToolApprovalResponse[],
  cursor: AgentUIStreamCursor,
): Promise<AgentUIMessageChunk[]> {
  const chunks: AgentUIMessageChunk[] = [];
  let storedToolResults: readonly StoredToolResult[] | undefined;
  for (const approval of staleApprovals) {
    // A tool approval's interrupt ID is its tool call ID.
    const toolCallId = approval.approvalId;
    const isActiveTurnToolCall =
      cursor.turnId === activeTurnId && activeTurnToolCalls.some((toolCall) => toolCall.toolCallId === toolCallId);
    const record =
      activeTurnId !== null && isActiveTurnToolCall
        ? await records.readToolCall(activeTurnId, toolCallId)
        : undefined;
    if (record !== undefined && !isFinalToolCallStatus(record.status)) {
      cursor.toolCalls.set(toolCallId, "approval_resolved");
      continue;
    }
    cursor.toolCalls.set(toolCallId, "finished");
    if (record !== undefined) {
      if (record.status === "succeeded") {
        chunks.push({ type: "tool-output-available", toolCallId, output: record.output ?? null, dynamic: true });
      } else if (record.status === "rejected") {
        chunks.push({ type: "tool-output-denied", toolCallId });
      } else {
        chunks.push({ type: "tool-output-error", toolCallId, errorText: describeToolCallOutcome(record), dynamic: true });
      }
      continue;
    }
    storedToolResults ??= await records.readRecentStoredToolResults();
    const storedToolOutput = [...storedToolResults]
      .reverse()
      .find(
        (result) =>
          result.toolCallId === toolCallId && (approval.turnId === undefined || result.turnId === approval.turnId),
      )?.toolOutput;
    chunks.push(
      storedToolOutput === undefined
        ? { type: "tool-output-error", toolCallId, errorText: staleApprovalText, dynamic: true }
        : { type: "tool-output-available", toolCallId, output: storedToolOutput, dynamic: true },
    );
  }
  return chunks;
}

/**
 * Returns a cursor at the Stream tail that continues the active turn the browser already shows. It records
 * where the turn started, so a reconnect with its token replays the whole turn.
 */
async function createActiveTurnContinuationCursor(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
  tailResumeToken: string,
  activeTurnId: string | null,
  toolCalls: readonly AgentToolCall[],
): Promise<AgentUIStreamCursor> {
  const cursor = createAgentUIStreamCursor(tailResumeToken, activeTurnId, true);
  if (activeTurnId === null) {
    cursor.isTurnFinished = true;
    return cursor;
  }
  cursor.turnStartResumeToken =
    (await findAgentEventResumeTokenBeforeTurn(client, flow, flowId, activeTurnId)) ?? null;
  for (const toolCall of toolCalls) {
    cursor.toolCalls.set(toolCall.toolCallId, toolCallUIStateOfRecord(toolCall));
  }
  return cursor;
}

function createAgentUIMessageSseResponse(
  options: AgentUIMessageStreamResponseOptions,
  start: AgentUIStreamStart,
  pollTimeoutMs: number,
  leadingChunks: readonly AgentUIMessageChunk[],
): Response {
  return createServerSentEventResponse(
    formatAgentUIMessageSseFrames(options, start, pollTimeoutMs, leadingChunks),
    agentUIMessageStreamHeaders,
    options.headers,
  );
}

async function* formatAgentUIMessageSseFrames(
  options: AgentUIMessageStreamOptions,
  start: AgentUIStreamStart,
  pollTimeoutMs: number,
  leadingChunks: readonly AgentUIMessageChunk[],
): AsyncGenerator<string, void, undefined> {
  const cursor = start.cursor;
  if (leadingChunks.length > 0) {
    const resumeTokenChunk: AgentUIMessageChunk = {
      type: agentResumeTokenDataPartType,
      data: encodeAgentUIStreamCursor(cursor),
      transient: true,
    };
    yield [...leadingChunks, resumeTokenChunk].map(formatUIMessageChunkFrame).join("");
  }
  if (cursor.isTurnFinished) {
    yield formatUIMessageChunkFrame({ type: "finish", finishReason: "other" });
  } else {
    for await (const chunks of readAgentUIMessageChunkBatches(options, start, pollTimeoutMs)) {
      if (chunks === undefined) {
        yield serverSentEventKeepAliveFrame;
      } else if (chunks.length > 0) {
        yield chunks.map(formatUIMessageChunkFrame).join("");
      }
    }
  }
  yield "data: [DONE]\n\n";
}

function formatUIMessageChunkFrame(chunk: AgentUIMessageChunk): string {
  return `data: ${JSON.stringify(chunk)}\n\n`;
}

function decodeResumeTokenOrUndefined(resumeToken: string): AgentUIStreamCursor | undefined {
  try {
    return decodeAgentUIStreamCursor(resumeToken);
  } catch (error: unknown) {
    if (error instanceof TypeError) {
      return undefined;
    }
    throw error;
  }
}

function createInvalidResumeTokenResponse(headers: Readonly<Record<string, string>> | undefined): Response {
  return new Response("the agent UI stream resume token is invalid", {
    status: 400,
    headers: mergeHeaders({ "content-type": "text/plain; charset=utf-8" }, headers),
  });
}

function isToolPartType(type: unknown): boolean {
  return type === "dynamic-tool" || (typeof type === "string" && type.startsWith("tool-"));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
