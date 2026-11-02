// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Client, StreamMessage } from "@superdurable/dex";

import type { AgentSessionFlow } from "../agent-session-flow.js";
import type { AgentEvent, AgentSnapshot } from "../agent-session-values.js";
import { convertAgentEventToJson, convertAgentSnapshotToJson } from "./agent-event-json.js";
import {
  findAgentEventResumeTokenBeforeTurn,
  probeAgentEventResumeToken,
  readAgentEventsTailResumeToken,
  readNextAgentEvent,
  requireAgentEventPollTimeoutMs,
} from "./agent-event-stream-positions.js";
import {
  createServerSentEventResponse,
  mergeHeaders,
  serverSentEventKeepAliveFrame,
} from "./server-sent-event-responses.js";

/** HTTP headers of the plain `AgentEvents` Server-Sent Events response. */
export const agentEventStreamHeaders: Readonly<Record<string, string>> = Object.freeze({
  "content-type": "text/event-stream",
  "cache-control": "no-cache",
  connection: "keep-alive",
  "x-accel-buffering": "no",
});

/**
 * Selects the agent session and Stream position of a plain `AgentEvents` Server-Sent Events stream.
 *
 * Pass it to {@link streamAgentEventSseFrames} or {@link createAgentEventSseResponse}.
 */
export interface AgentEventSseOptions {
  /** Client used for every Stream read. The caller keeps ownership. */
  readonly client: Client;
  /** The agent session Flow, the same object the Worker registered. */
  readonly flow: AgentSessionFlow;
  /** Session ID: the Flow ID of the agent session. */
  readonly flowId: string;
  /**
   * Dex resume token of the last event the browser received: the `Last-Event-ID` request header, or the
   * `resumeToken` of {@link readAgentEventStreamSnapshot} on the first connection. Omit it, or pass `""`,
   * to start at the oldest retained event.
   */
  readonly resumeToken?: string | undefined;
  /**
   * Long-poll duration of one Stream read, in milliseconds. It must be a whole number of seconds. The
   * stream sends a keep-alive comment after each empty poll. Defaults to 10,000.
   */
  readonly pollTimeoutMs?: number;
  /** Ends the stream before its next Stream read once aborted, such as the request's `signal`. */
  readonly signal?: AbortSignal | undefined;
}

/** A session snapshot and the Dex resume token that a plain event stream opened with it starts from. */
export interface AgentEventStreamSnapshot {
  /** Output of the `get_agent_snapshot` RPC. */
  readonly snapshot: AgentSnapshot;
  /**
   * Dex resume token to open the event stream with. While a turn is active it replays that turn's events
   * from its first retained event; otherwise it starts after the newest event read before the snapshot.
   */
  readonly resumeToken: string;
}

/** Snake_case wire JSON of an {@link AgentEventStreamSnapshot}, the same JSON as the Python core. */
export interface AgentEventStreamSnapshotJson {
  /** The `AgentSnapshot` wire JSON. */
  readonly snapshot: Readonly<Record<string, unknown>>;
  /** Dex resume token to open the event stream with. */
  readonly resume_token: string;
}

/**
 * Formats one `AgentEvents` message as a Server-Sent Event whose `id` is its Dex resume token.
 *
 * An `EventSource` sends the `id` back as the `Last-Event-ID` header when it reconnects. The event has no
 * `event:` field, so `EventSource.onmessage` receives every event; switch on `type` in the data.
 *
 * @param message - Message read from the `AgentEvents` Stream.
 * @returns `id: <resume token>`, then `data: <AgentEvent wire JSON>`, then a blank line.
 */
export function formatAgentEventSseFrame(message: StreamMessage<AgentEvent>): string {
  return `id: ${message.resumeToken}\ndata: ${JSON.stringify(convertAgentEventToJson(message.value))}\n\n`;
}

/**
 * Yields an agent session's `AgentEvents` as Server-Sent Event frames, resuming after a Dex resume token.
 *
 * Each event becomes one frame whose `id` is its Dex resume token and whose data is the `AgentEvent` wire
 * JSON: `type`, `turn_id`, `attempt`, `step_execution_id`, and `payload` text. Reconnecting with the last
 * `id` continues with the next event, with no repeated or missing event while the Stream retains them.
 * After each empty long poll it yields a keep-alive comment. The stream never ends on its own; stop
 * iterating, or abort `signal`, when the browser goes away.
 *
 * The browser applies the event rules of the session contract. Within one `step_execution_id` it keeps
 * only the highest `attempt` and drops the partial text of lower attempts. After `turn_completed` or
 * `turn_failed` it replaces the turn's live text with the stored messages. On every connection, load
 * {@link readAgentEventStreamSnapshot} first and open the stream from its `resumeToken`.
 *
 * @example
 * ```ts
 * for await (const frame of streamAgentEventSseFrames({
 *   client, flow, flowId: sessionId, resumeToken: request.headers["last-event-id"] })) {
 *   response.write(frame);
 * }
 * ```
 * @param options - Session, start position, and polling.
 * @returns An async generator of SSE frames and keep-alive comments.
 * @throws {RangeError} When `pollTimeoutMs` is not a positive whole number of seconds.
 * @throws {DexServiceError} When a Stream read fails.
 */
export async function* streamAgentEventSseFrames(
  options: AgentEventSseOptions,
): AsyncGenerator<string, void, undefined> {
  yield* formatAgentEventSseFramesFrom(options, undefined);
}

async function* formatAgentEventSseFramesFrom(
  options: AgentEventSseOptions,
  firstMessage: StreamMessage<AgentEvent> | undefined,
): AsyncGenerator<string, void, undefined> {
  const pollTimeoutMs = requireAgentEventPollTimeoutMs(options.pollTimeoutMs);
  let eventResumeToken = options.resumeToken ?? "";
  let pendingMessage = firstMessage;
  while (options.signal?.aborted !== true) {
    const message =
      pendingMessage ??
      (await readNextAgentEvent(options.client, options.flow, options.flowId, eventResumeToken, pollTimeoutMs));
    pendingMessage = undefined;
    if (message === undefined) {
      yield serverSentEventKeepAliveFrame;
      continue;
    }
    eventResumeToken = message.resumeToken;
    yield formatAgentEventSseFrame(message);
  }
}

/**
 * Returns a streaming `text/event-stream` Response of an agent session's `AgentEvents`.
 *
 * It writes {@link streamAgentEventSseFrames} and stops reading when the browser closes the response.
 * Mount it in any server that returns a Fetch API `Response`. The session ID usually comes from the
 * browser, so the route must check that the caller may read that session.
 *
 * Before it answers 200, it reads once, with a long poll of up to 1 second, to check the resume token.
 * When Dex rejects the token, such as one of another session, it answers 400, which stops an
 * `EventSource` from reconnecting with it; the browser then loads the snapshot again. A token from
 * before a Stream Store restart is accepted and continues at the oldest retained event.
 *
 * @example
 * ```ts
 * // GET /sessions/:id/events
 * export async function GET(request: Request, sessionId: string): Promise<Response> {
 *   const url = new URL(request.url);
 *   return createAgentEventSseResponse({
 *     client, flow, flowId: sessionId,
 *     resumeToken: request.headers.get("last-event-id") ?? url.searchParams.get("resumeToken") ?? "",
 *     signal: request.signal,
 *   });
 * }
 * // In the browser:
 * const { resumeToken } = await (await fetch(`/sessions/${sessionId}/snapshot`)).json();
 * const events = new EventSource(`/sessions/${sessionId}/events?resumeToken=${encodeURIComponent(resumeToken)}`);
 * events.onmessage = (message) => render(JSON.parse(message.data));
 * ```
 * @param options - Session, start position, polling, and extra response headers.
 * @returns A 200 Response with {@link agentEventStreamHeaders} and the application's `headers`; 400 with
 * a text body when Dex rejects the resume token.
 * @throws {RangeError} When `pollTimeoutMs` is not a positive whole number of seconds.
 * @throws {DexServiceError} When the first Stream read fails for another reason.
 */
export async function createAgentEventSseResponse(
  options: AgentEventSseOptions & { readonly headers?: Readonly<Record<string, string>> },
): Promise<Response> {
  requireAgentEventPollTimeoutMs(options.pollTimeoutMs);
  const probe = await probeAgentEventResumeToken(
    options.client,
    options.flow,
    options.flowId,
    options.resumeToken ?? "",
  );
  if (!probe.isAccepted) {
    return new Response("the resume token does not belong to this session; load the snapshot again", {
      status: 400,
      headers: mergeHeaders({ "content-type": "text/plain; charset=utf-8" }, options.headers),
    });
  }
  return createServerSentEventResponse(
    formatAgentEventSseFramesFrom(options, probe.firstMessage),
    agentEventStreamHeaders,
    options.headers,
  );
}

/**
 * Reads the session snapshot together with the Dex resume token to open the plain event stream from.
 *
 * Call it whenever the browser connects or reconnects after a gap. It reads the newest event first and
 * the snapshot second, so events after the token may repeat what the snapshot shows but none is missing.
 * While a turn is active the token replays that turn's events, so the browser can rebuild its live text.
 * The `get_agent_snapshot` RPC needs a running Worker.
 *
 * @example
 * ```ts
 * // GET /sessions/:id/snapshot
 * const value = await readAgentEventStreamSnapshot({ client, flow, flowId: sessionId });
 * return Response.json(convertAgentEventStreamSnapshotToJson(value));
 * ```
 * @param options - Client, Flow, and session ID.
 * @returns The snapshot and the resume token.
 * @throws {DexServiceError} When the Stream read or the RPC fails.
 */
export async function readAgentEventStreamSnapshot(options: {
  readonly client: Client;
  readonly flow: AgentSessionFlow;
  readonly flowId: string;
}): Promise<AgentEventStreamSnapshot> {
  const { client, flow, flowId } = options;
  const tailResumeToken = await readAgentEventsTailResumeToken(client, flow, flowId);
  const snapshot = await client.invokeRPC(flow.getAgentSnapshot, flowId);
  const activeTurnId = snapshot.state?.activeTurnId ?? null;
  if (activeTurnId === null) {
    return { snapshot, resumeToken: tailResumeToken };
  }
  const turnResumeToken = await findAgentEventResumeTokenBeforeTurn(client, flow, flowId, activeTurnId);
  return { snapshot, resumeToken: turnResumeToken ?? tailResumeToken };
}

/**
 * Converts an {@link AgentEventStreamSnapshot} to snake_case wire JSON for an HTTP response.
 *
 * @param value - Snapshot and resume token.
 * @returns `{ snapshot, resume_token }`, with the snapshot in its `AgentSnapshot` wire format.
 */
export function convertAgentEventStreamSnapshotToJson(
  value: AgentEventStreamSnapshot,
): AgentEventStreamSnapshotJson {
  return { snapshot: convertAgentSnapshotToJson(value.snapshot), resume_token: value.resumeToken };
}
