// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Most sessions that the `sessions` subcommand loads before it sorts them. */
export const sessionListRowLimit = 250;
/** Most SearchFlows requests that the `sessions` subcommand sends, so pages without sessions cannot loop forever. */
export const sessionListRequestLimit = 20;

/** One loaded page: its sessions in Dex order and the token of the next page, `""` on the last one. */
export interface SessionListPage<Session> {
  readonly sessions: readonly Session[];
  readonly nextPageToken: string;
}

/** The sessions that the `sessions` subcommand lists. */
export interface CollectedSessionList<Session> {
  /** At most {@link sessionListRowLimit} sessions, in Dex order. */
  readonly sessions: readonly Session[];
  /** Whether Dex has sessions that the list leaves out. */
  readonly hasMoreSessions: boolean;
}

/**
 * Loads pages until it has {@link sessionListRowLimit} sessions or a page has no next page token.
 *
 * A page may hold fewer sessions than its page size while more exist, so the count of pages is no bound.
 *
 * @param loadPage - Loads the page that a token names; `""` names the first page.
 * @returns The first sessions in Dex order, and whether more exist.
 */
export async function collectSessionsForList<Session>(
  loadPage: (nextPageToken: string) => Promise<SessionListPage<Session>>,
): Promise<CollectedSessionList<Session>> {
  const sessions: Session[] = [];
  let nextPageToken = "";
  for (let request = 0; request < sessionListRequestLimit; request += 1) {
    const page = await loadPage(nextPageToken);
    sessions.push(...page.sessions);
    nextPageToken = page.nextPageToken;
    if (nextPageToken === "" || sessions.length >= sessionListRowLimit) {
      break;
    }
  }
  const isTruncated = sessions.length > sessionListRowLimit;
  return {
    sessions: isTruncated ? sessions.slice(0, sessionListRowLimit) : sessions,
    hasMoreSessions: nextPageToken !== "" || isTruncated,
  };
}
