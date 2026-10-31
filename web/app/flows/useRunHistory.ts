// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useMemo, useRef, useState } from 'react';
import {
  appendToNewestHistoryWindow,
  createHistoryPageLoader,
  loadHistoryWindowAround,
  loadHistoryWindowFrom,
  loadLatestEventIdHint,
  loadNewestHistoryWindow,
  loadOlderHistoryWindow,
  loadOldestRetainedEvents,
  type HistoryWindow,
} from '@/lib/history';
import {
  inferPrunedEventIdRange,
  type EventIdRange,
  type ReportedRetainedHistory,
} from '@/lib/historyRelations';
import type { FlowHistoryEvent } from '@/lib/types';

/** A Timeline position that Back returns to. A null page follows the newest window. */
interface TimelineLocation {
  page: HistoryWindow | null;
  selectedEvent: FlowHistoryEvent | null;
}

export interface RunHistory {
  /** The newest window, which Live extends. */
  newest: HistoryWindow | null;
  /** The window the Timeline shows. */
  timelineWindow: HistoryWindow | null;
  isTimelineOnNewest: boolean;
  isLoadingTimelinePage: boolean;
  canGoBack: boolean;
  highlightedEventId: number | null;
  flowStartEvent: FlowHistoryEvent | null;
  prunedEventIdRange?: EventIdRange;
  clear: () => void;
  loadNewest: (runAttempt: number) => Promise<void>;
  appendNewest: (latestEventIdHint: number) => Promise<void>;
  showOlder: () => Promise<void>;
  showNewer: () => Promise<void>;
  showNewest: () => void;
  /** Returns the event to select, loading the page that holds it when needed. */
  jumpToEvent: (eventId: number, selectedEvent: FlowHistoryEvent | null) => Promise<FlowHistoryEvent | undefined>;
  /** Returns the event that was selected before the last jump. */
  back: () => FlowHistoryEvent | null;
  highlightEvent: (eventId: number | null) => void;
}

/** Keeps the run history bounded: one newest window for Live and one Timeline page. */
export function useRunHistory(flowId: string, runId: string): RunHistory {
  const [newest, setNewest] = useState<HistoryWindow | null>(null);
  const [timelinePage, setTimelinePage] = useState<HistoryWindow | null>(null);
  const [oldestRetainedEvents, setOldestRetainedEvents] = useState<FlowHistoryEvent[]>([]);
  const [reportedRetainedHistory, setReportedRetainedHistory] = useState<ReportedRetainedHistory>();
  const [backStack, setBackStack] = useState<TimelineLocation[]>([]);
  const [highlightedEventId, setHighlightedEventId] = useState<number | null>(null);
  const [pendingPageLoadCount, setPendingPageLoadCount] = useState(0);
  // Each clear invalidates loads that started earlier.
  const generation = useRef(0);
  const runAttemptRef = useRef(0);
  const newestRef = useRef<HistoryWindow | null>(null);

  const flowStartEvent = useMemo(() => (
    oldestRetainedEvents[0]?.type === 'FlowStartedOrContinued'
      ? oldestRetainedEvents[0]
      : newest?.events.find((event) => event.type === 'FlowStartedOrContinued') ?? null
  ), [newest, oldestRetainedEvents]);
  // A Server report wins over the inference, even when it reports that nothing is pruned.
  const prunedEventIdRange = useMemo(() => (
    reportedRetainedHistory
      ? reportedRetainedHistory.prunedEventIdRange
      : inferPrunedEventIdRange(oldestRetainedEvents)
  ), [oldestRetainedEvents, reportedRetainedHistory]);

  const pageLoader = useCallback(
    () => createHistoryPageLoader(flowId, runId, runAttemptRef.current),
    [flowId, runId],
  );

  const acceptWindow = useCallback((window: HistoryWindow) => {
    if (window.reportedRetainedHistory) setReportedRetainedHistory(window.reportedRetainedHistory);
  }, []);

  const replaceNewest = useCallback((window: HistoryWindow) => {
    newestRef.current = window;
    setNewest(window);
    acceptWindow(window);
  }, [acceptWindow]);

  const clear = useCallback(() => {
    generation.current += 1;
    newestRef.current = null;
    setNewest(null);
    setTimelinePage(null);
    setOldestRetainedEvents([]);
    setReportedRetainedHistory(undefined);
    setBackStack([]);
    setHighlightedEventId(null);
  }, []);

  const loadNewest = useCallback(async (runAttempt: number) => {
    const loadGeneration = generation.current;
    runAttemptRef.current = runAttempt;
    const loadPage = pageLoader();
    const [latestEventIdHint, oldest] = await Promise.all([
      loadLatestEventIdHint(flowId, runId),
      loadOldestRetainedEvents(loadPage),
    ]);
    const window = await loadNewestHistoryWindow(loadPage, latestEventIdHint);
    if (loadGeneration !== generation.current) return;
    setOldestRetainedEvents(oldest.events);
    if (oldest.reportedRetainedHistory) setReportedRetainedHistory(oldest.reportedRetainedHistory);
    replaceNewest(window);
  }, [flowId, pageLoader, replaceNewest, runId]);

  const appendNewest = useCallback(async (latestEventIdHint: number) => {
    const current = newestRef.current;
    if (!current) return;
    const loadGeneration = generation.current;
    const window = await appendToNewestHistoryWindow(pageLoader(), current, latestEventIdHint);
    if (loadGeneration !== generation.current || newestRef.current !== current) return;
    replaceNewest(window);
  }, [pageLoader, replaceNewest]);

  const loadTimelinePage = useCallback(async (load: () => Promise<HistoryWindow>) => {
    const loadGeneration = generation.current;
    setPendingPageLoadCount((count) => count + 1);
    try {
      const window = await load();
      if (loadGeneration !== generation.current) return undefined;
      acceptWindow(window);
      return window;
    } finally {
      setPendingPageLoadCount((count) => count - 1);
    }
  }, [acceptWindow]);

  const showOlder = useCallback(async () => {
    const base = timelinePage ?? newest;
    const firstEventId = base?.events[0]?.eventId;
    if (!base?.hasOlder || firstEventId === undefined) return;
    const window = await loadTimelinePage(() => (
      loadOlderHistoryWindow(pageLoader(), firstEventId, prunedEventIdRange)
    ));
    if (!window) return;
    setTimelinePage(window);
    setHighlightedEventId(null);
  }, [loadTimelinePage, newest, pageLoader, prunedEventIdRange, timelinePage]);

  const showNewer = useCallback(async () => {
    const lastEventId = timelinePage?.events.at(-1)?.eventId;
    if (!timelinePage?.hasNewer || lastEventId === undefined) return;
    const window = await loadTimelinePage(() => loadHistoryWindowFrom(pageLoader(), lastEventId + 1));
    if (!window) return;
    // The end of the lineage is the newest window, which Live keeps extending.
    setTimelinePage(window.hasNewer ? window : null);
    setHighlightedEventId(null);
  }, [loadTimelinePage, pageLoader, timelinePage]);

  const showNewest = useCallback(() => {
    setTimelinePage(null);
    setHighlightedEventId(null);
  }, []);

  const jumpToEvent = useCallback(async (eventId: number, selectedEvent: FlowHistoryEvent | null) => {
    const displayed = timelinePage ?? newest;
    const displayedEvent = displayed?.events.find((event) => event.eventId === eventId);
    if (displayedEvent) {
      setHighlightedEventId(eventId);
      return displayedEvent;
    }
    const newestEvent = newest?.events.find((event) => event.eventId === eventId);
    const window = newestEvent ? null : await loadTimelinePage(() => loadHistoryWindowAround(pageLoader(), eventId));
    const target = newestEvent ?? window?.events.find((event) => event.eventId === eventId);
    if (!target) return undefined;
    setBackStack((stack) => [...stack, { page: timelinePage, selectedEvent }]);
    setTimelinePage(window ?? null);
    setHighlightedEventId(eventId);
    return target;
  }, [loadTimelinePage, newest, pageLoader, timelinePage]);

  const back = useCallback(() => {
    const location = backStack.at(-1);
    if (!location) return null;
    setBackStack(backStack.slice(0, -1));
    setTimelinePage(location.page);
    setHighlightedEventId(location.selectedEvent?.eventId ?? null);
    return location.selectedEvent;
  }, [backStack]);

  const timelineWindow = timelinePage ?? newest;
  return useMemo(() => ({
    newest,
    timelineWindow,
    isTimelineOnNewest: timelinePage === null,
    isLoadingTimelinePage: pendingPageLoadCount > 0,
    canGoBack: backStack.length > 0,
    highlightedEventId,
    flowStartEvent,
    prunedEventIdRange,
    clear,
    loadNewest,
    appendNewest,
    showOlder,
    showNewer,
    showNewest,
    jumpToEvent,
    back,
    highlightEvent: setHighlightedEventId,
  }), [
    appendNewest,
    back,
    backStack.length,
    clear,
    flowStartEvent,
    highlightedEventId,
    jumpToEvent,
    loadNewest,
    newest,
    pendingPageLoadCount,
    prunedEventIdRange,
    showNewer,
    showNewest,
    showOlder,
    timelinePage,
    timelineWindow,
  ]);
}
