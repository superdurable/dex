// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { FlowHistoryEvent, HistoryPage } from './types';

interface CompleteHistory {
  events: FlowHistoryEvent[];
  nextInternalEventId: number;
}

export async function loadCompleteHistory(
  loadPage: (nextPageToken: string, startInternalEventId: number) => Promise<HistoryPage>,
  initialPageToken = '',
  initialInternalEventId = 0,
): Promise<CompleteHistory> {
  const events: FlowHistoryEvent[] = [];
  const seenPageTokens = new Set<string>();
  let nextPageToken = initialPageToken;
  let nextInternalEventId = initialInternalEventId;

  do {
    if (nextPageToken) {
      if (seenPageTokens.has(nextPageToken)) {
        throw new Error('History pagination returned a repeated page token');
      }
      seenPageTokens.add(nextPageToken);
    }
    const page = await loadPage(nextPageToken, nextInternalEventId);
    events.push(...page.events);
    nextPageToken = page.nextPageToken;
    nextInternalEventId = page.nextInternalEventId;
  } while (nextPageToken);

  return { events, nextInternalEventId };
}
