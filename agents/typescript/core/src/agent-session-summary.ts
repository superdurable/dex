// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Client, FlowStatus, SearchFlowEntry } from "@superdurable/dex";

import { sessionSummaryIndexKeys } from "./agent-session-persistence.js";

/**
 * Most Unicode code points {@link buildSessionTitle} keeps.
 *
 * @since 0.6.0
 */
export const sessionTitleMaxCodePoints = 80;

/**
 * Page size that {@link listAgentSessions} uses when the caller gives none.
 *
 * @since 0.6.0
 */
export const defaultSessionListPageSize = 50;

// Only ASCII whitespace, so every core collapses exactly the same characters.
const titleWhitespace = /[ \t\n\r\f\v]+/g;

/**
 * One session of a {@link listAgentSessions} page, read from its search entry.
 *
 * `title`, `lastActivityAt`, and `turnCount` are `null` when the session does not have them. A session that a release
 * before 0.6.0 started has no title. It gains the last activity time and the turn count when a 0.6.0 or later Worker
 * starts or ends one of its turns. Show such a missing value as a dash.
 *
 * @since 0.6.0
 */
export interface AgentSessionSummary {
  /** The session ID, which is the Flow ID. */
  readonly sessionId: string;
  /** The run ID of this Flow run. */
  readonly runId: string;
  /** The Flow status, such as `"running"`. */
  readonly status: FlowStatus;
  /** When the session started. */
  readonly startedAt: Date;
  /** When the session closed, or `undefined` while it runs. */
  readonly closedAt: Date | undefined;
  /** The `AgentSessionTitle` Attribute, or `null`. */
  readonly title: string | null;
  /** The `AgentSessionLastActivityAt` Attribute as a `Date`, or `null`. */
  readonly lastActivityAt: Date | null;
  /** The `AgentSessionTurnCount` Attribute, or `null`. */
  readonly turnCount: number | null;
}

/**
 * One page of {@link listAgentSessions}.
 *
 * @since 0.6.0
 */
export interface AgentSessionSummaryPage {
  /**
   * The sessions of the page, in Dex search order: running sessions first, then the most recently closed, then the
   * most recently started.
   */
  readonly sessions: readonly AgentSessionSummary[];
  /** Pass it to {@link listAgentSessions} for the next page; `""` on the last page. */
  readonly nextPageToken: string;
}

/**
 * Which page of which sessions {@link listAgentSessions} returns.
 *
 * @since 0.6.0
 */
export interface ListAgentSessionsOptions {
  /** Most sessions in the page. Defaults to {@link defaultSessionListPageSize}, 50. */
  readonly pageSize?: number;
  /** {@link AgentSessionSummaryPage.nextPageToken} of the previous page, or `""`, the default, for the first page. */
  readonly nextPageToken?: string;
  /**
   * Words the title must contain, compared without case. Omitted or blank lists every session. A word is a run of
   * letters and digits, and only whole words match: `flaky` finds "Fix the flaky test", `flak` does not. Sessions
   * without a title never match. Pass the same value with each page token.
   */
  readonly titleSearch?: string;
}

/**
 * Returns the title a session takes from its first user message.
 *
 * Each run of ASCII spaces, tabs, and line breaks becomes one space, leading and trailing spaces are removed, and the
 * text is cut to {@link sessionTitleMaxCodePoints} code points, without a trailing space. The Python core gives the
 * same title for the same text.
 *
 * @example
 * ```ts
 * buildSessionTitle("  Fix the\n flaky test "); // "Fix the flaky test"
 * ```
 *
 * @param firstMessageText - Text of the session's first user message.
 * @returns The title, or `null` when the text has no other character.
 * @since 0.6.0
 */
export function buildSessionTitle(firstMessageText: string): string | null {
  const collapsed = removeEdgeSpaces(firstMessageText.replace(titleWhitespace, " "));
  const title = removeEdgeSpaces(Array.from(collapsed).slice(0, sessionTitleMaxCodePoints).join(""));
  return title === "" ? null : title;
}

/**
 * Returns one page of the sessions of a session Flow type.
 *
 * It makes one `Client.searchFlows` call. It needs no Worker and reads no snapshot. Dex orders the page; it cannot
 * sort by last activity, so sort the sessions you loaded with {@link sortAgentSessionsByLastActivity}.
 *
 * @example
 * ```ts
 * let page = await listAgentSessions(client, flow.getFlowType(), { titleSearch: "refund" });
 * while (page.nextPageToken !== "") {
 *   page = await listAgentSessions(client, flow.getFlowType(), { titleSearch: "refund", nextPageToken: page.nextPageToken });
 * }
 * ```
 *
 * @param client - An open Client. Its Registry does not need the session Flow.
 * @param flowType - The session Flow type, such as `flow.getFlowType()`.
 * @param options - The page size, page token, and title search.
 * @returns The page of sessions.
 * @throws {RangeError} When `pageSize` is negative.
 * @throws {Error} When Dex rejects the search. A title search fails with `INVALID_ARGUMENT` while no Worker of 0.6.0
 * or later has started on the server, because the title index does not exist yet.
 * @since 0.6.0
 */
export async function listAgentSessions(
  client: Client,
  flowType: string,
  options: ListAgentSessionsOptions = {},
): Promise<AgentSessionSummaryPage> {
  let query = `FlowType = ${quoteQueryString(flowType)}`;
  const titleSearch = options.titleSearch;
  if (titleSearch !== undefined && titleSearch.trim() !== "") {
    query += ` AND ${sessionSummaryIndexKeys.title} = ${quoteQueryString(titleSearch)}`;
  }
  const page = await client.searchFlows(
    query,
    options.pageSize ?? defaultSessionListPageSize,
    options.nextPageToken ?? "",
  );
  return { sessions: page.flows.map(readSessionSummary), nextPageToken: page.nextPageToken };
}

/**
 * Returns sessions with the most recent activity first.
 *
 * A session without a last activity time sorts by its start time. Sessions with the same time sort by session ID.
 *
 * @param sessions - Sessions from one or more {@link listAgentSessions} pages.
 * @returns A new array, most recent first.
 * @since 0.6.0
 */
export function sortAgentSessionsByLastActivity(sessions: readonly AgentSessionSummary[]): AgentSessionSummary[] {
  return [...sessions].sort(
    (left, right) =>
      activityTime(right) - activityTime(left) ||
      (left.sessionId < right.sessionId ? -1 : left.sessionId > right.sessionId ? 1 : 0),
  );
}

function readSessionSummary(entry: SearchFlowEntry): AgentSessionSummary {
  const title = entry.indexedAttributes.get(sessionSummaryIndexKeys.title);
  const lastActivityAt = entry.indexedAttributes.get(sessionSummaryIndexKeys.lastActivityAt);
  const turnCount = entry.indexedAttributes.get(sessionSummaryIndexKeys.turnCount);
  return {
    sessionId: entry.flowId,
    runId: entry.runId,
    status: entry.status,
    startedAt: entry.startedAt,
    closedAt: entry.closedAt,
    title: typeof title === "string" ? title : null,
    lastActivityAt: typeof lastActivityAt === "string" ? parseTime(lastActivityAt) : null,
    turnCount: typeof turnCount === "bigint" ? Number(turnCount) : null,
  };
}

function parseTime(text: string): Date | null {
  const time = Date.parse(text);
  return Number.isNaN(time) ? null : new Date(time);
}

function activityTime(session: AgentSessionSummary): number {
  return (session.lastActivityAt ?? session.startedAt).getTime();
}

function removeEdgeSpaces(text: string): string {
  return text.replace(/^ +| +$/g, "");
}

function quoteQueryString(text: string): string {
  return `"${text.replaceAll("\\", "\\\\").replaceAll('"', '\\"')}"`;
}
