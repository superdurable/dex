// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { webURL } from '@/lib/basePath';
import { CLONE_FORK_POINT_TYPE, ID_REUSE_POLICY } from '@/lib/cloneFork';
import { readResponseJSON } from '@/lib/http';
import { isOpenFlowStatusCode } from '../work-queue/liveness';
import type { ConversationItem } from './sessionModel';
import type { LoadedAgentSession } from './useAgentSession';

const SESSION_TASK_QUEUE_PREFIX = 's-';
const SESSION_TASK_QUEUE_HASH_LENGTH = 20;
/** A CLI core session ID: harness name, UTC date and time, and 6 hex characters. */
const CLI_SESSION_ID = /^(.+)-\d{8}-\d{6}-[0-9a-f]{6}$/;

/**
 * The Attributes a forked session deletes, so it starts released, as a new session does.
 * See SessionTerminalLease and SessionLastTerminalEnvironment in agents/conformance/session_graph.json.
 */
export const SESSION_FORK_DELETED_ATTRIBUTES: readonly string[] = [
  'SessionTerminalLease',
  'SessionLastTerminalEnvironment',
];

/** Where a fork copies the source session: its current state, or right after one turn ended. */
export type SessionForkPoint = { kind: 'current' } | { kind: 'event'; eventId: number };

/**
 * Returns a session's task queue: "s-" and the first 20 lowercase hex characters of the
 * SHA-256 of the session ID in UTF-8. See agents/conformance/cli_session_task_queues.json.
 * Rejects where Web Crypto is unavailable, such as a page served over plain HTTP.
 */
export async function sessionTaskQueue(sessionId: string): Promise<string> {
  if (typeof crypto === 'undefined' || !crypto.subtle) {
    throw new Error('This browser cannot compute the session task queue: Web Crypto needs HTTPS or localhost.');
  }
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(sessionId));
  return `${SESSION_TASK_QUEUE_PREFIX}${hexOf(new Uint8Array(digest)).slice(0, SESSION_TASK_QUEUE_HASH_LENGTH)}`;
}

/**
 * Suggests a new session ID in the CLI core's format: the source's harness name, the UTC time
 * as YYYYMMDD-HHMMSS, and 6 random hex characters. A source ID in another format is the prefix.
 */
export function suggestedForkSessionId(sourceSessionId: string, now: Date, randomHex: string): string {
  const prefix = CLI_SESSION_ID.exec(sourceSessionId)?.[1] ?? sourceSessionId;
  const stamp = now.toISOString().slice(0, 19).replace(/[-:]/g, '').replace('T', '-');
  return `${prefix}-${stamp}-${randomHex}`;
}

/** Returns 6 random lowercase hex characters for a suggested session ID. */
export function randomSessionIdSuffix(): string {
  const bytes = new Uint8Array(3);
  crypto.getRandomValues(bytes);
  return hexOf(bytes);
}

/** A session can fork its current state only while it is open and between turns. */
export function canForkCurrentSession(session: LoadedAgentSession): boolean {
  const { state, waitingCalls } = session.view;
  return isOpenFlowStatusCode(session.summary.flowStatusCode)
    && state?.status === 'waiting_for_user_message'
    && !state.activeTurnId
    && waitingCalls.length === 0;
}

/** The event to fork right after, for the message that ended a turn; null for any other item. */
export function forkEventIdOf(item: ConversationItem, activeTurnId: string | null | undefined): number | null {
  if (item.kind !== 'message' || item.turnEndEventId === undefined) return null;
  return item.message.turnId === activeTurnId ? null : item.turnEndEventId;
}

/**
 * The fork's task queue override, or null to inherit the source's task queue.
 * A source on its own session task queue, as the Agents CLI starts it, moves the fork to the
 * new session's own task queue. Any other source, such as one on the default task queue,
 * keeps the Workers that run it.
 */
export async function sessionForkTaskQueue(
  sourceSessionId: string,
  sourceTaskQueue: string,
  sessionId: string,
): Promise<string | null> {
  if (sourceTaskQueue !== await sessionTaskQueue(sourceSessionId)) return null;
  return sessionTaskQueue(sessionId);
}

export interface SessionForkRequest {
  sourceSessionId: string;
  sourceRunId: string;
  /** The source run's task queue; '' for the default task queue. */
  sourceTaskQueue: string;
  sessionId: string;
  point: SessionForkPoint;
  reason: string;
  requestId: string;
}

/** The POST /api/flows/clone-fork body that forks a session between turns. */
export async function sessionForkPayload(request: SessionForkRequest): Promise<Record<string, unknown>> {
  const taskQueue = await sessionForkTaskQueue(request.sourceSessionId, request.sourceTaskQueue, request.sessionId);
  return {
    sourceFlowId: request.sourceSessionId,
    sourceRunId: request.sourceRunId,
    flowId: request.sessionId,
    requestId: request.requestId,
    ...(request.point.kind === 'current'
      ? { pointType: CLONE_FORK_POINT_TYPE.CURRENT }
      : { pointType: CLONE_FORK_POINT_TYPE.EVENT_ID, eventId: String(request.point.eventId) }),
    ...(taskQueue === null ? {} : { taskQueue }),
    deleteAttributeKeys: SESSION_FORK_DELETED_ATTRIBUTES,
    idReusePolicy: ID_REUSE_POLICY.DISALLOW_REUSE,
    reason: request.reason,
  };
}

/** Forks a session through the Web API and returns the new session's run ID. */
export async function forkAgentSession(request: SessionForkRequest, fetcher: typeof fetch = fetch): Promise<string> {
  const response = await fetcher(webURL('/api/flows/clone-fork'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(await sessionForkPayload(request)),
  });
  const data = await readResponseJSON<{ runId?: string }>(response);
  return data.runId ?? '';
}

function hexOf(bytes: Uint8Array): string {
  return [...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}
