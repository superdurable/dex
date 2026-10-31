// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  isEventIdInRange,
  reportedRetainedHistoryOf,
  type EventIdRange,
  type ReportedRetainedHistory,
} from './historyRelations';
import { readResponseJSON } from './http';
import { RUN_ATTEMPT_SEARCH_PARAM } from './timeTravel';
import type { FlowHistoryEvent, HistoryPage } from './types';

/** The most events one history window holds, and so the most one Timeline page shows. */
export const HISTORY_WINDOW_EVENT_LIMIT = 1000;

const LATEST_EVENT_ID_HINT_TIMEOUT_MS = 5000;

export interface HistoryPageRequest {
  startInternalEventId: number;
  estimatePageSize: number;
  nextPageToken: string;
}

export type HistoryPageLoader = (request: HistoryPageRequest) => Promise<HistoryPage>;

/** A contiguous slice of one run attempt lineage. */
export interface HistoryWindow {
  /** Events in ascending event ID order. */
  events: FlowHistoryEvent[];
  hasOlder: boolean;
  hasNewer: boolean;
  /** The event ID after the newest loaded event. */
  nextInternalEventId: number;
  /** The newest retained-history boundary that a page response reported. */
  reportedRetainedHistory?: ReportedRetainedHistory;
}

export function createHistoryPageLoader(flowId: string, runId: string, runAttempt: number): HistoryPageLoader {
  return async ({ startInternalEventId, estimatePageSize, nextPageToken }) => {
    const params = new URLSearchParams({
      flowId,
      runId,
      startInternalEventId: String(startInternalEventId),
      estimatePageSize: String(estimatePageSize),
    });
    if (nextPageToken) params.set('nextPageToken', nextPageToken);
    if (runAttempt > 0) params.set(RUN_ATTEMPT_SEARCH_PARAM, String(runAttempt));
    return readResponseJSON<HistoryPage>(await fetch(`/api/flows/history?${params}`, { cache: 'no-store' }));
  };
}

/**
 * Waits from event 1, which FlowStart satisfies, so the wait returns at once.
 *
 * Returns the run's newest event ID, or 0 when the Server does not report it.
 */
export async function loadLatestEventIdHint(flowId: string, runId: string): Promise<number> {
  const params = new URLSearchParams({ flowId, runId, nextInternalEventId: '1' });
  // A Server that long-polls this wait must not delay the history.
  const signal = typeof AbortSignal.timeout === 'function'
    ? AbortSignal.timeout(LATEST_EVENT_ID_HINT_TIMEOUT_MS)
    : undefined;
  try {
    return latestEventIdOfWait(
      await readResponseJSON<unknown>(await fetch(`/api/flows/wait?${params}`, { cache: 'no-store', signal })),
    );
  } catch (hintError) {
    console.warn('Unable to locate the newest history event; reading history from the start.', hintError);
    return 0;
  }
}

/** Returns the newest event ID a history wait response reports, or 0 when it reports none. */
export function latestEventIdOfWait(response: unknown): number {
  const value = response && typeof response === 'object'
    ? (response as Record<string, unknown>).availableInternalEventId
    : undefined;
  const eventId = typeof value === 'string' && value.trim() !== '' ? Number(value) : value;
  return typeof eventId === 'number' && Number.isSafeInteger(eventId) && eventId > 0 ? eventId : 0;
}

/** Reads FlowStart and the oldest retained event after it. */
export async function loadOldestRetainedEvents(loadPage: HistoryPageLoader): Promise<{
  events: FlowHistoryEvent[];
  reportedRetainedHistory?: ReportedRetainedHistory;
}> {
  const page = await loadPage(historyPageRequest(1, 2));
  return {
    events: sortedUniqueEvents(page.events).slice(0, 2),
    reportedRetainedHistory: reportedRetainedHistoryOf(page),
  };
}

/** Loads the newest window with one page read when the hint is current; otherwise reads forward. */
export async function loadNewestHistoryWindow(
  loadPage: HistoryPageLoader,
  latestEventIdHint: number,
  limit = HISTORY_WINDOW_EVENT_LIMIT,
): Promise<HistoryWindow> {
  if (latestEventIdHint <= 0) {
    return readToNewestWindow(loadPage, await loadPage(historyPageRequest(1, limit)), limit);
  }
  const start = Math.max(1, latestEventIdHint - limit + 1);
  const page = await loadPage(historyPageRequest(start, limit));
  if (page.events.length > 0 || start === 1) return readToNewestWindow(loadPage, page, limit);
  // An earlier run attempt's lineage ends before the run's newest event.
  const lastEventId = await findLastEventIdBefore(loadPage, start, limit);
  if (lastEventId === 0) return readToNewestWindow(loadPage, page, limit);
  return readToNewestWindow(
    loadPage,
    await loadPage(historyPageRequest(Math.max(1, lastEventId - limit + 1), limit)),
    limit,
  );
}

/** Loads up to `limit` events starting at an event ID. */
export async function loadHistoryWindowFrom(
  loadPage: HistoryPageLoader,
  startEventId: number,
  limit = HISTORY_WINDOW_EVENT_LIMIT,
): Promise<HistoryWindow> {
  const start = Math.max(1, startEventId);
  const page = await loadPage(historyPageRequest(start, limit));
  const sorted = sortedUniqueEvents(page.events);
  const events = sorted.slice(0, limit);
  return {
    events,
    hasOlder: events.length > 0 ? events[0].eventId > 1 : start > 1,
    hasNewer: Boolean(page.nextPageToken) || sorted.length > limit,
    nextInternalEventId: events.length < sorted.length
      ? events[events.length - 1].eventId + 1
      : page.nextInternalEventId,
    reportedRetainedHistory: reportedRetainedHistoryOf(page),
  };
}

/** Loads a window that holds an event near its middle, for a jump to that event. */
export async function loadHistoryWindowAround(
  loadPage: HistoryPageLoader,
  eventId: number,
  limit = HISTORY_WINDOW_EVENT_LIMIT,
): Promise<HistoryWindow> {
  return loadHistoryWindowFrom(loadPage, Math.max(1, eventId - Math.floor(limit / 2)), limit);
}

/**
 * Loads up to `limit` events before an event ID.
 *
 * Returns the oldest window instead when only FlowStart precedes that event.
 */
export async function loadOlderHistoryWindow(
  loadPage: HistoryPageLoader,
  beforeEventId: number,
  prunedEventIdRange: EventIdRange | undefined,
  limit = HISTORY_WINDOW_EVENT_LIMIT,
): Promise<HistoryWindow> {
  let start = Math.max(1, beforeEventId - limit);
  if (isEventIdInRange(prunedEventIdRange, start)) start = 1;
  let older = await readEventsBefore(loadPage, start, beforeEventId, limit);
  if (older.events.length === 0 && start > 1) {
    // Time travel leaves event IDs that the lineage skips.
    const lastEventId = await findLastEventIdBefore(loadPage, start, limit);
    if (lastEventId > 0) {
      const reportedRetainedHistory = older.reportedRetainedHistory;
      older = await readEventsBefore(loadPage, Math.max(1, lastEventId - limit + 1), lastEventId + 1, limit);
      older.reportedRetainedHistory ??= reportedRetainedHistory;
    }
  }
  if (older.events.length === 0 || (older.events.length === 1 && older.events[0].eventId === 1)) {
    return loadHistoryWindowFrom(loadPage, 1, limit);
  }
  const olderEvents = older.events.slice(-limit);
  return {
    events: olderEvents,
    hasOlder: olderEvents[0].eventId > 1,
    hasNewer: true,
    nextInternalEventId: olderEvents[olderEvents.length - 1].eventId + 1,
    reportedRetainedHistory: older.reportedRetainedHistory,
  };
}

/** Appends new events, keeping the newest `limit`; reloads the window when the hint is a window ahead. */
export async function appendToNewestHistoryWindow(
  loadPage: HistoryPageLoader,
  newestWindow: HistoryWindow,
  latestEventIdHint: number,
  limit = HISTORY_WINDOW_EVENT_LIMIT,
): Promise<HistoryWindow> {
  if (latestEventIdHint >= newestWindow.nextInternalEventId + limit) {
    return loadNewestHistoryWindow(loadPage, latestEventIdHint, limit);
  }
  const page = await loadPage(historyPageRequest(newestWindow.nextInternalEventId, limit));
  if (page.nextPageToken) return readToNewestWindow(loadPage, page, limit);
  const events = sortedUniqueEvents([...newestWindow.events, ...page.events]).slice(-limit);
  return {
    events,
    hasOlder: events.length > 0 && events[0].eventId > 1,
    hasNewer: false,
    nextInternalEventId: Math.max(newestWindow.nextInternalEventId, page.nextInternalEventId),
    reportedRetainedHistory: reportedRetainedHistoryOf(page) ?? newestWindow.reportedRetainedHistory,
  };
}

/** Puts FlowStart in front of a window that does not hold it, for views that read the start event. */
export function withFlowStart(
  events: FlowHistoryEvent[],
  flowStart: FlowHistoryEvent | null | undefined,
): FlowHistoryEvent[] {
  if (!flowStart || (events.length > 0 && events[0].eventId <= flowStart.eventId)) return events;
  return [flowStart, ...events];
}

function historyPageRequest(
  startInternalEventId: number,
  estimatePageSize: number,
  nextPageToken = '',
): HistoryPageRequest {
  return { startInternalEventId, estimatePageSize, nextPageToken };
}

/** Follows page tokens to the end of the lineage, keeping only the newest `limit` events. */
async function readToNewestWindow(
  loadPage: HistoryPageLoader,
  firstPage: HistoryPage,
  limit: number,
): Promise<HistoryWindow> {
  const seenPageTokens = new Set<string>();
  let page = firstPage;
  let events = sortedUniqueEvents(page.events).slice(-limit);
  let reportedRetainedHistory = reportedRetainedHistoryOf(page);
  while (page.nextPageToken) {
    if (seenPageTokens.has(page.nextPageToken)) {
      throw new Error('History pagination returned a repeated page token');
    }
    seenPageTokens.add(page.nextPageToken);
    page = await loadPage(historyPageRequest(page.nextInternalEventId, limit, page.nextPageToken));
    events = sortedUniqueEvents([...events, ...page.events]).slice(-limit);
    reportedRetainedHistory = reportedRetainedHistoryOf(page) ?? reportedRetainedHistory;
  }
  return {
    events,
    hasOlder: events.length > 0 && events[0].eventId > 1,
    hasNewer: false,
    nextInternalEventId: page.nextInternalEventId,
    reportedRetainedHistory,
  };
}

/** Reads events in [startEventId, beforeEventId), following page tokens until a read reaches the end. */
async function readEventsBefore(
  loadPage: HistoryPageLoader,
  startEventId: number,
  beforeEventId: number,
  limit: number,
): Promise<{ events: FlowHistoryEvent[]; reportedRetainedHistory?: ReportedRetainedHistory }> {
  const seenPageTokens = new Set<string>();
  let page = await loadPage(historyPageRequest(startEventId, limit));
  let reportedRetainedHistory = reportedRetainedHistoryOf(page);
  const events = page.events.filter((event) => event.eventId < beforeEventId);
  while (page.nextPageToken && !page.events.some((event) => event.eventId >= beforeEventId)) {
    if (seenPageTokens.has(page.nextPageToken)) {
      throw new Error('History pagination returned a repeated page token');
    }
    seenPageTokens.add(page.nextPageToken);
    page = await loadPage(historyPageRequest(page.nextInternalEventId, limit, page.nextPageToken));
    events.push(...page.events.filter((event) => event.eventId < beforeEventId));
    reportedRetainedHistory = reportedRetainedHistoryOf(page) ?? reportedRetainedHistory;
  }
  return { events: sortedUniqueEvents(events), reportedRetainedHistory };
}

/**
 * Searches backward with one-event reads: exponential steps, then bisection.
 *
 * Returns the newest lineage event ID below `upperEventId`, or 0 when there is none.
 */
async function findLastEventIdBefore(
  loadPage: HistoryPageLoader,
  upperEventId: number,
  initialDistance: number,
): Promise<number> {
  const firstEventIdFrom = async (startEventId: number) => (
    (await loadPage(historyPageRequest(startEventId, 1))).events[0]?.eventId
  );
  // Invariant: no lineage event lies in [high, upperEventId).
  let high = upperEventId;
  let low = 0;
  for (let distance = Math.max(1, initialDistance); low === 0; distance *= 2) {
    if (high <= 1) return 0;
    const start = Math.max(1, high - distance);
    const eventId = await firstEventIdFrom(start);
    if (eventId !== undefined && eventId < high) low = eventId;
    else high = start;
  }
  while (high - low > 1) {
    const middle = Math.floor((low + high) / 2);
    const eventId = await firstEventIdFrom(middle);
    if (eventId !== undefined && eventId < high) low = eventId;
    else high = middle;
  }
  return low;
}

function sortedUniqueEvents(events: FlowHistoryEvent[]): FlowHistoryEvent[] {
  return [...new Map(events.map((event) => [event.eventId, event])).values()]
    .sort((left, right) => left.eventId - right.eventId);
}
