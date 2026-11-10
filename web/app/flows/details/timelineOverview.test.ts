// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  brushPixelSpan,
  bucketOverviewEvents,
  buildOverviewAxis,
  eventIndexToRatio,
  hasRetriedFailure,
  minimumPositionGap,
  monotonicEventTimes,
  ratioToEventIndex,
  type EventStatus,
} from './timelineOverview';

const baseMs = Date.parse('2026-11-09T10:00:00Z');

function eventAt(eventId: number, offsetMs: number | null, type: FlowHistoryEvent['type'] = 'StepExecuteCompleted', context: Record<string, unknown> = {}): FlowHistoryEvent {
  return {
    eventId,
    eventTime: offsetMs === null ? '' : new Date(baseMs + offsetMs).toISOString(),
    type,
    payload: { context: { stepType: 'Tick', stepExecutionId: `Tick-${eventId}`, ...context } },
  };
}

describe('timeline overview axis', () => {
  it('places events by time, so a burst and a quiet gap show', () => {
    // Ten events in the first second, then one event nine seconds later.
    const events = [...Array.from({ length: 10 }, (_, index) => eventAt(index + 1, index * 100)), eventAt(11, 10_000)];
    const axis = buildOverviewAxis(events, 100);
    expect(axis.kind).toBe('time');
    expect(axis.startMs).toBe(baseMs);
    expect(axis.endMs).toBe(baseMs + 10_000);
    expect(axis.positions[0]).toBe(0);
    expect(axis.positions[10]).toBe(1);
    expect(axis.positions[5]).toBeCloseTo(0.05);

    const buckets = bucketOverviewEvents(axis, events.map(() => 'normal'), events.map(() => false), 100);
    expect(buckets.reduce((sum, bucket) => sum + bucket.count, 0)).toBe(11);
    expect(buckets[0]).toMatchObject({ firstEventIndex: 0, lastEventIndex: 0, count: 1 });
    expect(buckets[9]).toMatchObject({ firstEventIndex: 9, lastEventIndex: 9, count: 1 });
    expect(buckets.slice(10, 99).every((bucket) => bucket.count === 0)).toBe(true);
    expect(buckets[99]).toMatchObject({ firstEventIndex: 10, count: 1 });
  });

  it('falls back to event order when every event shares one timestamp', () => {
    const events = Array.from({ length: 4 }, (_, index) => eventAt(index + 1, 0));
    const axis = buildOverviewAxis(events, 8);
    expect(axis.kind).toBe('index');
    // The page's time range stays known, so the axis labels can still name it.
    expect(axis.startMs).toBe(baseMs);
    expect(axis.endMs).toBe(baseMs);
    expect(axis.positions).toEqual([0.125, 0.375, 0.625, 0.875]);
    expect(axis.edges).toEqual([0, 0.25, 0.5, 0.75, 1]);
    const buckets = bucketOverviewEvents(axis, ['normal', 'normal', 'normal', 'normal'], [false, false, false, false], 8);
    expect(buckets.map((bucket) => bucket.count)).toEqual([0, 1, 0, 1, 0, 1, 0, 1]);
  });

  it('falls back to event order when one time bucket would hold more than half the events', () => {
    const events = [...Array.from({ length: 9 }, (_, index) => eventAt(index + 1, index)), eventAt(10, 3_600_000)];
    expect(buildOverviewAxis(events, 200).kind).toBe('index');
  });

  it('falls back to event order without two valid timestamps', () => {
    const axis = buildOverviewAxis([eventAt(1, null), eventAt(2, null), eventAt(3, 5)], 10);
    expect(axis.kind).toBe('index');
    expect(axis.startMs).toBeNull();
  });

  it('falls back to event order when a short page needs room for each event and time would crowd them', () => {
    // Seven events within 300 ms, then one 9.3 s later.
    const crowded = [...Array.from({ length: 7 }, (_, index) => eventAt(index + 1, index * 50)), eventAt(8, 9300)];
    expect(buildOverviewAxis(crowded, 100).kind).toBe('time');
    expect(buildOverviewAxis(crowded, 100, 0.01).kind).toBe('index');
    const spread = Array.from({ length: 8 }, (_, index) => eventAt(index + 1, index * 1000));
    const axis = buildOverviewAxis(spread, 100, 0.01);
    expect(axis.kind).toBe('time');
    expect(minimumPositionGap(axis)).toBeCloseTo(1 / 7);
  });

  it('keeps event times monotonic when a timestamp is missing or earlier', () => {
    expect(monotonicEventTimes([eventAt(1, 0), eventAt(2, null), eventAt(3, 50), eventAt(4, 20), eventAt(5, 90)]))
      .toEqual([baseMs, baseMs, baseMs + 50, baseMs + 50, baseMs + 90]);
  });

  it('counts each status per bucket and counts retried Steps apart from failed ones', () => {
    const events = Array.from({ length: 6 }, (_, index) => eventAt(index + 1, 0));
    const statuses: EventStatus[] = ['normal', 'failed', 'waiting', 'canceled', 'normal', 'normal'];
    const retried = [false, true, false, false, true, false];
    const [bucket] = bucketOverviewEvents(buildOverviewAxis(events, 1), statuses, retried, 1);
    // A failed event stays failed even when it carries attempt data.
    expect(bucket).toMatchObject({ count: 6, failed: 1, retried: 1, waiting: 1, canceled: 1, firstEventIndex: 0, lastEventIndex: 5 });
  });
});

describe('retried failure detection', () => {
  it('marks Step events that succeeded after failed attempts', () => {
    expect(hasRetriedFailure(eventAt(1, 0, 'StepExecuteCompleted', { finalAttempt: 2 }))).toBe(true);
    expect(hasRetriedFailure(eventAt(1, 0, 'StepWaitForCompleted', { lastFailureInfo: { attempt: 1, backendError: 'boom' } }))).toBe(true);
    expect(hasRetriedFailure(eventAt(1, 0, 'StepExecuteCompleted', { finalAttempt: 1 }))).toBe(false);
    expect(hasRetriedFailure(eventAt(1, 0, 'StepExecuteCompleted', { lastFailureInfo: {} }))).toBe(false);
    expect(hasRetriedFailure(eventAt(1, 0, 'StepExecuteCompleted'))).toBe(false);
  });

  it('leaves Failed events and non-Step events to their own status', () => {
    expect(hasRetriedFailure(eventAt(1, 0, 'StepExecuteFailed', { finalAttempt: 3 }))).toBe(false);
    expect(hasRetriedFailure(eventAt(1, 0, 'RpcExecutionCompleted', { finalAttempt: 3 }))).toBe(false);
  });
});

describe('overview brush mapping', () => {
  it('maps the index axis like a plain slider', () => {
    const axis = buildOverviewAxis(Array.from({ length: 1000 }, (_, index) => eventAt(index + 1, 0)), 150);
    expect(ratioToEventIndex(axis, 0.5)).toBeCloseTo(500);
    expect(eventIndexToRatio(axis, 250)).toBeCloseTo(0.25);
    expect(ratioToEventIndex(axis, -1)).toBe(0);
    expect(ratioToEventIndex(axis, 2)).toBe(1000);
  });

  it('round-trips event indexes through the time axis', () => {
    const offsets = [0, 10, 20, 30, 5000, 5010, 9990, 10_000];
    const axis = buildOverviewAxis(offsets.map((offset, index) => eventAt(index + 1, offset)), 40);
    expect(axis.kind).toBe('time');
    for (const index of [0, 1.5, 3, 4.25, 6, 8]) {
      expect(ratioToEventIndex(axis, eventIndexToRatio(axis, index))).toBeCloseTo(index);
    }
    // The quiet gap between events 4 and 5 covers about half the track.
    expect(eventIndexToRatio(axis, 4) - eventIndexToRatio(axis, 3.999)).toBeGreaterThan(0);
    expect(eventIndexToRatio(axis, 4)).toBeCloseTo((0.003 + 0.5) / 2, 3);
  });

  it('keeps the brush at least its minimum width and inside the track', () => {
    const axis = buildOverviewAxis(Array.from({ length: 1000 }, (_, index) => eventAt(index + 1, 0)), 150);
    const wide = brushPixelSpan(axis, 500, 100, 1000, 24);
    expect(wide.left).toBeCloseTo(500);
    expect(wide.right).toBeCloseTo(600);
    const narrow = brushPixelSpan(axis, 500, 5, 1000, 24);
    expect(narrow.right - narrow.left).toBeCloseTo(24);
    expect((narrow.left + narrow.right) / 2).toBeCloseTo(502.5);
    expect(brushPixelSpan(axis, 998, 2, 1000, 24)).toEqual({ left: 976, right: 1000 });
    expect(brushPixelSpan(axis, 0, 2, 1000, 24)).toEqual({ left: 0, right: 24 });
  });
});
