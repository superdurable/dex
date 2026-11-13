// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { webURL } from '@/lib/basePath';
import { definitionRevisionHeaders, readResponseJSON } from '@/lib/http';
import { SESSION_COPY } from './copy';
import { classifyActionFailure, type SessionActionOutcome, type SessionControlContext } from './sessionActions';
import type { AgentSessionSchema } from './sessionSchema';

/** The largest compact JSON text of session settings, in UTF-8 bytes, that the agents Clients accept. */
export const MAX_SESSION_SETTINGS_BYTES = 16 * 1024;

const AGENT_SESSIONS_API = '/api/v2/agent-sessions';

export interface SessionRouteContext {
  flowType: string;
  flowId: string;
  workQueuePermissions: readonly string[];
  definitionRevision: string;
}

/** Queues one user message through send_user_message; it starts the next turn when none runs. */
export function sendSessionMessage(
  context: SessionRouteContext,
  text: string,
  senderName: string | null = null,
  fetcher: typeof fetch = fetch,
): Promise<SessionActionOutcome> {
  const fields = senderName === null ? { text } : { text, senderName };
  return postSessionRoute(context, 'messages', fields, SESSION_COPY.sendRefused, fetcher);
}

/** Replaces the session's settings through update_session_settings, from the next turn on. */
export function updateSessionSettings(
  context: SessionRouteContext,
  sessionSettings: Record<string, unknown>,
  fetcher: typeof fetch = fetch,
): Promise<SessionActionOutcome> {
  return postSessionRoute(context, 'settings', { sessionSettings }, SESSION_COPY.settingsRefused, fetcher);
}

export interface StartSessionRequest {
  flowType: string;
  flowId: string;
  initialMessage: string;
  sessionSettings: Record<string, unknown> | null;
  definitionRevision: string;
}

/** Starts one session with the session start input; resolves to its Flow ID. */
export async function startAgentSession(request: StartSessionRequest, fetcher: typeof fetch = fetch): Promise<string> {
  const response = await fetcher(webURL(`${AGENT_SESSIONS_API}/start`), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...definitionRevisionHeaders(request.definitionRevision) },
    body: JSON.stringify({
      flowType: request.flowType,
      flowId: request.flowId,
      initialMessage: request.initialMessage.trim() === '' ? null : request.initialMessage,
      sessionSettings: request.sessionSettings,
    }),
  });
  return (await readResponseJSON<{ flowId: string }>(response)).flowId;
}

async function postSessionRoute(
  context: SessionRouteContext,
  route: 'messages' | 'settings',
  fields: Record<string, unknown>,
  refusedMessage: string,
  fetcher: typeof fetch,
): Promise<SessionActionOutcome> {
  let output: unknown;
  try {
    const response = await fetcher(webURL(`${AGENT_SESSIONS_API}/${route}`), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...definitionRevisionHeaders(context.definitionRevision) },
      body: JSON.stringify({
        flowType: context.flowType,
        flowId: context.flowId,
        workQueuePermissions: context.workQueuePermissions,
        ...fields,
      }),
    });
    output = (await readResponseJSON<{ output?: unknown }>(response)).output;
  } catch (error) {
    return classifyActionFailure(error);
  }
  return output === false ? { kind: 'refused', message: refusedMessage } : { kind: 'done' };
}

/** The permission the session's Actions require; sending and settings use it too. */
export function sessionActionPermission(schema: AgentSessionSchema): string | null {
  const actions = [schema.resolveInterrupt, schema.steerUserMessage, schema.cancelTurn, schema.answerQuestion];
  return actions.find((action) => action !== undefined)?.requiredPermission ?? null;
}

/** Why sending a message or saving settings cannot run now, or null when it can. */
export function sessionRouteBlockedReason(
  schema: AgentSessionSchema,
  rpcName: string,
  context: SessionControlContext,
): string | null {
  if (!schema.rpcNames.includes(rpcName)) return SESSION_COPY.noSessionRPC(rpcName);
  const permission = sessionActionPermission(schema);
  if (permission === null) return SESSION_COPY.noSessionActions;
  if (!context.isFlowOpen) return SESSION_COPY.closed;
  if (!context.workQueuePermissions.includes(permission)) return SESSION_COPY.chooseWorkingAs(permission);
  return null;
}

export type SessionSettingsParse =
  | { ok: true; value: Record<string, unknown> }
  | { ok: false; message: string };

/** Reads settings text as one JSON object within the size limit; blank text is no settings. */
export function parseSessionSettings(text: string, blankAllowed: boolean): SessionSettingsParse {
  if (text.trim() === '') {
    return blankAllowed ? { ok: true, value: {} } : { ok: false, message: SESSION_COPY.settingsNotObject };
  }
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error) {
    return { ok: false, message: SESSION_COPY.settingsNotJSON(error instanceof Error ? error.message : String(error)) };
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ok: false, message: SESSION_COPY.settingsNotObject };
  }
  return checkSessionSettingsSize(value as Record<string, unknown>);
}

/** Checks the compact JSON size of settings against MAX_SESSION_SETTINGS_BYTES. */
export function checkSessionSettingsSize(value: Record<string, unknown>): SessionSettingsParse {
  const size = new TextEncoder().encode(JSON.stringify(value)).length;
  if (size > MAX_SESSION_SETTINGS_BYTES) {
    return { ok: false, message: SESSION_COPY.settingsTooLarge(size, MAX_SESSION_SETTINGS_BYTES) };
  }
  return { ok: true, value };
}

/** A new session ID: a random UUID, which never holds a character a Flow ID reserves. */
export function newSessionId(): string {
  return `session-${crypto.randomUUID()}`;
}
