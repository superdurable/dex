// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import {
  appendToNewestHistoryWindow,
  latestEventIdOfWait,
  loadHistoryWindowAround,
  loadHistoryWindowFrom,
  loadNewestHistoryWindow,
  loadOlderHistoryWindow,
  withFlowStart,
} from './history';
import { historyTestServer, sequentialStepHistory, stepEvent } from './historyTestServer';
import type { FlowHistoryEvent } from './types';

function eventIds(events: FlowHistoryEvent[]): number[] {
  return events.map((event) => event.eventId);
}

function range(first: number, last: number): number[] {
  return Array.from({ length: last - first + 1 }, (_, offset) => first + offset);
}

describe('history windows', () => {
  it('locates the newest window of a 20,000-event history with one page read', async () => {
    const server = historyTestServer(sequentialStepHistory(20_000));

    const window = await loadNewestHistoryWindow(server.loadPage, 20_000);

    expect(eventIds(window.events)).toEqual(range(19_001, 20_000));
    expect(window).toMatchObject({ hasOlder: true, hasNewer: false, nextInternalEventId: 20_001 });
    expect(server.requests).toEqual([{ startInternalEventId: 19_001, estimatePageSize: 1000, nextPageToken: '' }]);
  });

  it('follows newer pages when the hint is stale and keeps only the newest window', async () => {
    const server = historyTestServer(sequentialStepHistory(3500));

    const window = await loadNewestHistoryWindow(server.loadPage, 1200);

    expect(eventIds(window.events)).toEqual(range(2501, 3500));
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([201, 1201, 2201, 3201]);
  });

  it('reads forward from the start without a hint', async () => {
    const server = historyTestServer(sequentialStepHistory(2500));

    const window = await loadNewestHistoryWindow(server.loadPage, 0);

    expect(eventIds(window.events)).toEqual(range(1501, 2500));
    expect(server.requests.every((request) => request.estimatePageSize === 1000)).toBe(true);
  });

  it('finds the end of an earlier run attempt lineage below the run newest event', async () => {
    const server = historyTestServer(sequentialStepHistory(4000));

    const window = await loadNewestHistoryWindow(server.loadPage, 30_000);

    expect(eventIds(window.events)).toEqual(range(3001, 4000));
    expect(server.requests.length).toBeLessThan(40);
    expect(server.requests.every((request) => request.estimatePageSize <= 1000)).toBe(true);
  });

  it('pages older and newer windows by event ID', async () => {
    const server = historyTestServer(sequentialStepHistory(2500));
    const newest = await loadNewestHistoryWindow(server.loadPage, 2500);

    const older = await loadOlderHistoryWindow(server.loadPage, newest.events[0].eventId, undefined);
    expect(eventIds(older.events)).toEqual(range(501, 1500));
    expect(older).toMatchObject({ hasOlder: true, hasNewer: true });

    const oldest = await loadOlderHistoryWindow(server.loadPage, older.events[0].eventId, undefined);
    expect(eventIds(oldest.events)).toEqual(range(1, 500));
    expect(oldest.hasOlder).toBe(false);

    const newer = await loadHistoryWindowFrom(server.loadPage, oldest.events.at(-1)!.eventId + 1);
    expect(eventIds(newer.events)).toEqual(range(501, 1500));
    expect(newer.hasNewer).toBe(true);
  });

  it('shows FlowStart with the oldest retained events instead of alone', async () => {
    const events = sequentialStepHistory(5000).filter((event) => event.eventId === 1 || event.eventId >= 4001);
    const server = historyTestServer(events);
    const pruned = { firstEventId: 2, lastEventId: 4000 };

    const oldest = await loadOlderHistoryWindow(server.loadPage, 4001, pruned);

    expect(eventIds(oldest.events)).toEqual([1, ...range(4001, 4999)]);
    expect(oldest.hasOlder).toBe(false);
  });

  it('skips event IDs that time travel left out of the lineage', async () => {
    const events = [
      ...sequentialStepHistory(300),
      ...sequentialStepHistory(5300).filter((event) => event.eventId > 5000).map((event) => ({ ...event, runAttempt: 2 })),
    ];
    const server = historyTestServer(events);

    const older = await loadOlderHistoryWindow(server.loadPage, 5001, undefined);

    expect(eventIds(older.events)).toEqual(range(1, 300));
  });

  it('follows page tokens when a Server returns short pages for an older window', async () => {
    const server = historyTestServer(sequentialStepHistory(2500));
    const shortPages: typeof server.loadPage = (request) => server.loadPage({
      ...request,
      estimatePageSize: Math.min(request.estimatePageSize, 300),
    });

    const older = await loadOlderHistoryWindow(shortPages, 1501, undefined);

    expect(eventIds(older.events)).toEqual(range(501, 1500));
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([501, 801, 1101, 1401]);
  });

  it('loads a window around a jump target', async () => {
    const server = historyTestServer(sequentialStepHistory(20_000));

    const window = await loadHistoryWindowAround(server.loadPage, 7000);

    expect(window.events.some((event) => event.eventId === 7000)).toBe(true);
    expect(window.events).toHaveLength(1000);
  });

  it('appends Live events to the newest window without rereading it', async () => {
    const server = historyTestServer(sequentialStepHistory(1500));
    const newest = await loadNewestHistoryWindow(server.loadPage, 1500);
    server.events.push(stepEvent(1501, 'StepWaitForCompleted', { stepExecutionId: 'Step-751' }));
    server.requests.length = 0;

    const appended = await appendToNewestHistoryWindow(server.loadPage, newest, 1501);

    expect(eventIds(appended.events)).toEqual(range(502, 1501));
    expect(appended.nextInternalEventId).toBe(1502);
    expect(server.requests).toEqual([{ startInternalEventId: 1501, estimatePageSize: 1000, nextPageToken: '' }]);
  });

  it('reloads the newest window when Live falls more than one window behind', async () => {
    const server = historyTestServer(sequentialStepHistory(5000));
    const stale = await loadHistoryWindowFrom(server.loadPage, 1);
    server.requests.length = 0;

    const appended = await appendToNewestHistoryWindow(server.loadPage, { ...stale, hasNewer: false }, 5000);

    expect(eventIds(appended.events)).toEqual(range(4001, 5000));
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([4001]);
  });

  it('rejects a repeated page token', async () => {
    await expect(loadNewestHistoryWindow(async () => ({
      events: [],
      nextPageToken: 'repeated',
      nextInternalEventId: 1,
    }), 0)).rejects.toThrow('repeated page token');
  });

  it('reads the newest event ID from protojson and numeric wait responses', () => {
    expect(latestEventIdOfWait({ eventAvailable: true, availableInternalEventId: '20000' })).toBe(20_000);
    expect(latestEventIdOfWait({ availableInternalEventId: 42 })).toBe(42);
    expect(latestEventIdOfWait({ eventAvailable: true })).toBe(0);
    expect(latestEventIdOfWait(null)).toBe(0);
  });

  it('puts FlowStart before a window that does not hold it', () => {
    const [flowStart, ...rest] = sequentialStepHistory(5);
    expect(eventIds(withFlowStart(rest.slice(2), flowStart))).toEqual([1, 4, 5]);
    expect(eventIds(withFlowStart([flowStart, ...rest], flowStart))).toEqual([1, 2, 3, 4, 5]);
  });
});
