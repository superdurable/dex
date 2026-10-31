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
  isEventIdInRange,
  prunedEventIdRangeOf,
  type EventIdRange,
  type ReportedRetainedHistory,
} from '@/lib/historyRelations';
import type { FlowHistoryEvent } from '@/lib/types';

/** The most Timeline positions that Back remembers. */
const TIMELINE_BACK_STACK_LIMIT = 20;

/** A Timeline position that Back reloads. A null start follows the newest window. */
interface TimelineLocation {
  pageStartEventId: number | null;
  selectedEventId: number | null;
}

/** What Back restored, or that a later navigation superseded it and nothing changed. */
export type TimelineBackResult =
  | { kind: 'restored'; selectedEvent: FlowHistoryEvent | null }
  | { kind: 'superseded' };

/** What a jump found: the event, an event the history window removed, or nothing. */
export type TimelineJumpResult =
  | { kind: 'loaded'; event: FlowHistoryEvent }
  | { kind: 'pruned' }
  | { kind: 'missing' }
  | { kind: 'superseded' };

export interface RunHistory {
  /** The newest window, which Live extends. */
  newest: HistoryWindow | null;
  /** The window the Timeline shows. */
  timelineWindow: HistoryWindow | null;
  isTimelineOnNewest: boolean;
  /** Whether events newer than the Timeline page exist, including ones Live added since it loaded. */
  timelineHasNewer: boolean;
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
  /** Loads the page that holds an event when needed. */
  jumpToEvent: (eventId: number, selectedEvent: FlowHistoryEvent | null) => Promise<TimelineJumpResult>;
  /** Restores the page and the event selected before the last jump. */
  back: () => Promise<TimelineBackResult>;
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
  // Only the latest Timeline navigation may change the page.
  const navigationSequence = useRef(0);
  const runAttemptRef = useRef(0);
  const newestRef = useRef<HistoryWindow | null>(null);

  const flowStartEvent = useMemo(() => (
    oldestRetainedEvents[0]?.type === 'FlowStartedOrContinued'
      ? oldestRetainedEvents[0]
      : newest?.events.find((event) => event.type === 'FlowStartedOrContinued') ?? null
  ), [newest, oldestRetainedEvents]);
  const prunedEventIdRange = useMemo(
    () => prunedEventIdRangeOf(reportedRetainedHistory, oldestRetainedEvents),
    [oldestRetainedEvents, reportedRetainedHistory],
  );
  const timelineLastEventId = timelinePage?.events.at(-1)?.eventId ?? 0;
  const newestLastEventId = newest?.events.at(-1)?.eventId ?? 0;
  const timelineHasNewer = timelinePage !== null
    && (timelinePage.hasNewer || newestLastEventId > timelineLastEventId);

  const pageLoader = useCallback(
    () => createHistoryPageLoader(flowId, runId, runAttemptRef.current),
    [flowId, runId],
  );

  const acceptOldestRetainedEvents = useCallback((events: FlowHistoryEvent[]) => {
    const oldest = events.slice(0, 2);
    setOldestRetainedEvents((current) => (
      current.length === oldest.length && current.every((event, index) => event.eventId === oldest[index].eventId)
        ? current
        : oldest
    ));
  }, []);

  // Any window that starts at FlowStart also shows the current pruned gap.
  const acceptWindow = useCallback((historyWindow: HistoryWindow) => {
    if (historyWindow.reportedRetainedHistory) setReportedRetainedHistory(historyWindow.reportedRetainedHistory);
    if (historyWindow.events[0]?.eventId === 1) acceptOldestRetainedEvents(historyWindow.events);
  }, [acceptOldestRetainedEvents]);

  const replaceNewest = useCallback((historyWindow: HistoryWindow) => {
    newestRef.current = historyWindow;
    setNewest(historyWindow);
    acceptWindow(historyWindow);
  }, [acceptWindow]);

  const clear = useCallback(() => {
    generation.current += 1;
    navigationSequence.current += 1;
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
    const newestWindow = await loadNewestHistoryWindow(loadPage, latestEventIdHint);
    if (loadGeneration !== generation.current) return;
    acceptOldestRetainedEvents(oldest.events);
    if (oldest.reportedRetainedHistory) setReportedRetainedHistory(oldest.reportedRetainedHistory);
    replaceNewest(newestWindow);
  }, [acceptOldestRetainedEvents, flowId, pageLoader, replaceNewest, runId]);

  const appendNewest = useCallback(async (latestEventIdHint: number) => {
    const current = newestRef.current;
    if (!current) return;
    const loadGeneration = generation.current;
    const newestWindow = await appendToNewestHistoryWindow(pageLoader(), current, latestEventIdHint);
    if (loadGeneration !== generation.current || newestRef.current !== current) return;
    replaceNewest(newestWindow);
  }, [pageLoader, replaceNewest]);

  /** Returns undefined when a later navigation or a clear superseded this load. */
  const loadTimelinePage = useCallback(async <T>(load: () => Promise<T>): Promise<T | undefined> => {
    const loadGeneration = generation.current;
    const sequence = ++navigationSequence.current;
    setPendingPageLoadCount((count) => count + 1);
    try {
      const loaded = await load();
      return loadGeneration === generation.current && sequence === navigationSequence.current
        ? loaded
        : undefined;
    } finally {
      setPendingPageLoadCount((count) => count - 1);
    }
  }, []);

  const showTimelinePage = useCallback((historyWindow: HistoryWindow | null) => {
    if (historyWindow) acceptWindow(historyWindow);
    setTimelinePage(historyWindow);
    setHighlightedEventId(null);
  }, [acceptWindow]);

  const showOlder = useCallback(async () => {
    const base = timelinePage ?? newest;
    const firstEventId = base?.events[0]?.eventId;
    if (!base?.hasOlder || firstEventId === undefined) return;
    const olderWindow = await loadTimelinePage(() => (
      loadOlderHistoryWindow(pageLoader(), firstEventId, prunedEventIdRange)
    ));
    if (olderWindow) showTimelinePage(olderWindow);
  }, [loadTimelinePage, newest, pageLoader, prunedEventIdRange, showTimelinePage, timelinePage]);

  const showNewer = useCallback(async () => {
    if (!timelineHasNewer) return;
    const newerWindow = await loadTimelinePage(() => loadHistoryWindowFrom(pageLoader(), timelineLastEventId + 1));
    // The fetched page stays, even at the end, so the view does not switch to another window.
    if (newerWindow && newerWindow.events.length > 0) showTimelinePage(newerWindow);
  }, [loadTimelinePage, pageLoader, showTimelinePage, timelineHasNewer, timelineLastEventId]);

  const showNewest = useCallback(() => {
    navigationSequence.current += 1;
    showTimelinePage(null);
  }, [showTimelinePage]);

  const rememberLocation = useCallback((selectedEvent: FlowHistoryEvent | null) => {
    const location = {
      pageStartEventId: timelinePage?.events[0]?.eventId ?? null,
      selectedEventId: selectedEvent?.eventId ?? null,
    };
    setBackStack((stack) => [...stack, location].slice(-TIMELINE_BACK_STACK_LIMIT));
  }, [timelinePage]);

  const jumpToEvent = useCallback(async (
    eventId: number,
    selectedEvent: FlowHistoryEvent | null,
  ): Promise<TimelineJumpResult> => {
    const displayedEvent = (timelinePage ?? newest)?.events.find((event) => event.eventId === eventId);
    if (displayedEvent) {
      setHighlightedEventId(eventId);
      return { kind: 'loaded', event: displayedEvent };
    }
    const newestEvent = newest?.events.find((event) => event.eventId === eventId);
    if (newestEvent) {
      navigationSequence.current += 1;
      rememberLocation(selectedEvent);
      setTimelinePage(null);
      setHighlightedEventId(eventId);
      return { kind: 'loaded', event: newestEvent };
    }
    const loadPage = pageLoader();
    const jumpWindow = await loadTimelinePage(() => loadHistoryWindowAround(loadPage, eventId));
    if (!jumpWindow) return { kind: 'superseded' };
    const target = jumpWindow.events.find((event) => event.eventId === eventId);
    if (target) {
      rememberLocation(selectedEvent);
      acceptWindow(jumpWindow);
      setTimelinePage(jumpWindow);
      setHighlightedEventId(eventId);
      return { kind: 'loaded', event: target };
    }
    // The history window may have pruned the event since it was classified.
    const loadGeneration = generation.current;
    const oldest = await loadOldestRetainedEvents(loadPage);
    if (loadGeneration !== generation.current) return { kind: 'superseded' };
    acceptOldestRetainedEvents(oldest.events);
    if (oldest.reportedRetainedHistory) setReportedRetainedHistory(oldest.reportedRetainedHistory);
    const currentPrunedRange = prunedEventIdRangeOf(
      oldest.reportedRetainedHistory ?? reportedRetainedHistory,
      oldest.events,
    );
    return isEventIdInRange(currentPrunedRange, eventId) ? { kind: 'pruned' } : { kind: 'missing' };
  }, [
    acceptOldestRetainedEvents,
    acceptWindow,
    loadTimelinePage,
    newest,
    pageLoader,
    rememberLocation,
    reportedRetainedHistory,
    timelinePage,
  ]);

  const back = useCallback(async (): Promise<TimelineBackResult> => {
    const location = backStack.at(-1);
    if (!location) return { kind: 'superseded' };
    const findSelected = (events: FlowHistoryEvent[] | undefined) => (
      events?.find((event) => event.eventId === location.selectedEventId) ?? null
    );
    if (location.pageStartEventId === null) {
      navigationSequence.current += 1;
      setBackStack(backStack.slice(0, -1));
      setTimelinePage(null);
      setHighlightedEventId(location.selectedEventId);
      return { kind: 'restored', selectedEvent: findSelected(newestRef.current?.events) };
    }
    const startEventId = location.pageStartEventId;
    const previousWindow = await loadTimelinePage(() => loadHistoryWindowFrom(pageLoader(), startEventId));
    // A superseded reload keeps the entry, so Back still returns there later.
    if (!previousWindow) return { kind: 'superseded' };
    setBackStack((stack) => (stack.at(-1) === location ? stack.slice(0, -1) : stack));
    acceptWindow(previousWindow);
    setTimelinePage(previousWindow);
    setHighlightedEventId(location.selectedEventId);
    return { kind: 'restored', selectedEvent: findSelected(previousWindow.events) };
  }, [acceptWindow, backStack, loadTimelinePage, pageLoader]);

  const timelineWindow = timelinePage ?? newest;
  return useMemo(() => ({
    newest,
    timelineWindow,
    isTimelineOnNewest: timelinePage === null,
    timelineHasNewer,
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
    timelineHasNewer,
    timelinePage,
    timelineWindow,
  ]);
}
