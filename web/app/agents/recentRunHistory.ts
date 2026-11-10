// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  appendToNewestHistoryWindow,
  createHistoryPageLoader,
  loadLatestEventIdHint,
  loadNewestHistoryWindow,
  loadOldestRetainedEvents,
  withFlowStart,
  type HistoryWindow,
} from '@/lib/history';
import type { FlowHistoryEvent } from '@/lib/types';

/** The newest history window of one run; executions before it are absent from the overlay. */
export interface RecentRunHistory {
  runId: string;
  runAttempt: number;
  flowStart: FlowHistoryEvent | null;
  historyWindow: HistoryWindow;
}

/** Loads the newest window, or appends to `previous` when it belongs to the same run attempt. */
export async function loadRecentRunHistory(
  flowId: string,
  runId: string,
  runAttempt = 0,
  previous?: RecentRunHistory,
): Promise<RecentRunHistory> {
  const loadPage = createHistoryPageLoader(flowId, runId, 0);
  if (previous && previous.runId === runId && previous.runAttempt === runAttempt) {
    return {
      ...previous,
      historyWindow: await appendToNewestHistoryWindow(loadPage, previous.historyWindow, 0),
    };
  }
  const [latestEventIdHint, oldest] = await Promise.all([
    loadLatestEventIdHint(flowId, runId),
    loadOldestRetainedEvents(loadPage),
  ]);
  const flowStart = oldest.events[0]?.type === 'FlowStartedOrContinued' ? oldest.events[0] : null;
  return {
    runId,
    runAttempt,
    flowStart,
    historyWindow: await loadNewestHistoryWindow(loadPage, latestEventIdHint),
  };
}

export function recentRunEvents(history: RecentRunHistory): FlowHistoryEvent[] {
  return withFlowStart(history.historyWindow.events, history.flowStart);
}
