// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowHistoryEvent } from '@/lib/types';

/** Only states that need attention get a status; everything else is normal. */
export type EventStatus = 'normal' | 'waiting' | 'failed' | 'canceled';

/** How the overview places events along its x axis. */
export type OverviewAxisKind = 'time' | 'index';

/** One histogram bar: the events whose positions fall in one slice of the x axis. */
export interface OverviewBucket {
  /** Index of the first and last event in the bucket, or -1 when the bucket is empty. */
  firstEventIndex: number;
  lastEventIndex: number;
  count: number;
  failed: number;
  /** Step events that had failed attempts, whatever their status; a Failed event counts as failed instead. */
  retried: number;
  /** The retried events with no other status, which the bar draws in the failure color. */
  retriedNormal: number;
  waiting: number;
  canceled: number;
}

/** Where the overview draws each event and how its histogram groups them. */
export interface OverviewAxis {
  kind: OverviewAxisKind;
  /** Each event's position in [0, 1], non-decreasing in event order. */
  positions: number[];
  /** Event i spans edges[i] to edges[i + 1]; on the index axis, edges[i] is exactly i / count. */
  edges: number[];
  /** The first and last event times in epoch milliseconds, on either axis; null without two valid timestamps. */
  startMs: number | null;
  endMs: number | null;
  /** Each event's time from monotonicEventTimes, or null without two valid timestamps. */
  eventTimes: number[] | null;
}

/** A time axis cannot spread events when one bucket would hold more than this share of them. */
const maxTimeBucketShare = 0.5;

function contextRecord(event: FlowHistoryEvent): Record<string, unknown> {
  const context = event.payload.context;
  return context && typeof context === 'object' ? context as Record<string, unknown> : {};
}

/** Reports whether a Step event succeeded only after failed attempts. */
// Retries leave no Failed event, only finalAttempt above 1 and lastFailureInfo.
export function hasRetriedFailure(event: FlowHistoryEvent): boolean {
  const isStepEvent = event.type.startsWith('StepExecute') || event.type.startsWith('StepWaitFor');
  if (!isStepEvent || event.type.endsWith('Failed')) return false;
  const context = contextRecord(event);
  const finalAttempt = Number(context.finalAttempt);
  if (Number.isFinite(finalAttempt) && finalAttempt > 1) return true;
  const lastFailure = context.lastFailureInfo;
  return Boolean(lastFailure && typeof lastFailure === 'object' && Object.keys(lastFailure).length > 0);
}

/**
 * Returns non-decreasing event times in epoch milliseconds, or null when fewer than two parse.
 * A missing or earlier timestamp repeats the previous event's time.
 */
export function monotonicEventTimes(events: readonly FlowHistoryEvent[]): number[] | null {
  const parsed = events.map((event) => (event.eventTime ? Date.parse(event.eventTime) : Number.NaN));
  const firstValid = parsed.findIndex(Number.isFinite);
  if (firstValid < 0 || parsed.filter(Number.isFinite).length < 2) return null;
  let previous = parsed[firstValid];
  return parsed.map((time) => {
    if (Number.isFinite(time) && time > previous) previous = time;
    return previous;
  });
}

function bucketOf(position: number, bucketCount: number): number {
  return Math.min(bucketCount - 1, Math.max(0, Math.floor(position * bucketCount)));
}

function edgesFromPositions(positions: readonly number[]): number[] {
  const edges = [0];
  for (let index = 1; index < positions.length; index += 1) edges.push((positions[index - 1] + positions[index]) / 2);
  edges.push(1);
  return edges;
}

/** The smallest distance between neighboring event positions, as a ratio of the track; 1 for a single event. */
export function minimumPositionGap(axis: OverviewAxis): number {
  let gap = 1;
  for (let index = 1; index < axis.positions.length; index += 1) {
    gap = Math.min(gap, axis.positions[index] - axis.positions[index - 1]);
  }
  return gap;
}

/** Returns a time axis when event times spread the events, and an event-order axis otherwise. */
// Event order wins when one bucket holds over half the events, or neighbors sit closer than minEventGap.
export function buildOverviewAxis(
  events: readonly FlowHistoryEvent[],
  bucketCount: number,
  minEventGap = 0,
): OverviewAxis {
  const count = events.length;
  const times = monotonicEventTimes(events);
  const indexAxis = (): OverviewAxis => {
    const positions = events.map((_, index) => (index + 0.5) / count);
    return {
      kind: 'index',
      positions,
      edges: edgesFromPositions(positions),
      startMs: times ? times[0] : null,
      endMs: times ? times[count - 1] : null,
      eventTimes: times,
    };
  };
  if (!times) return indexAxis();
  const startMs = times[0];
  const endMs = times[count - 1];
  const span = endMs - startMs;
  if (span <= 0) return indexAxis();
  const positions = times.map((time) => (time - startMs) / span);
  const perBucket = new Map<number, number>();
  let busiest = 0;
  for (const position of positions) {
    const bucket = bucketOf(position, bucketCount);
    const next = (perBucket.get(bucket) ?? 0) + 1;
    perBucket.set(bucket, next);
    busiest = Math.max(busiest, next);
  }
  if (count > 1 && busiest > count * maxTimeBucketShare) return indexAxis();
  const axis: OverviewAxis = {
    kind: 'time',
    positions,
    edges: edgesFromPositions(positions),
    startMs,
    endMs,
    eventTimes: times,
  };
  return minEventGap > 0 && minimumPositionGap(axis) < minEventGap ? indexAxis() : axis;
}

/** Groups events into bucketCount equal slices of the axis and counts each status per slice. */
export function bucketOverviewEvents(
  axis: OverviewAxis,
  statuses: readonly EventStatus[],
  retried: readonly boolean[],
  bucketCount: number,
): OverviewBucket[] {
  const buckets: OverviewBucket[] = Array.from({ length: bucketCount }, () => ({
    firstEventIndex: -1,
    lastEventIndex: -1,
    count: 0,
    failed: 0,
    retried: 0,
    retriedNormal: 0,
    waiting: 0,
    canceled: 0,
  }));
  axis.positions.forEach((position, index) => {
    const bucket = buckets[bucketOf(position, bucketCount)];
    if (bucket.firstEventIndex < 0) bucket.firstEventIndex = index;
    bucket.lastEventIndex = index;
    bucket.count += 1;
    const status = statuses[index] ?? 'normal';
    if (status === 'failed') bucket.failed += 1;
    else if (status === 'waiting') bucket.waiting += 1;
    else if (status === 'canceled') bucket.canceled += 1;
    if (status !== 'failed' && retried[index]) {
      bucket.retried += 1;
      if (status === 'normal') bucket.retriedNormal += 1;
    }
  });
  return buckets;
}

/** Maps a fractional event index, such as the view's first slot, to its x ratio in [0, 1]. */
export function eventIndexToRatio(axis: OverviewAxis, eventIndex: number): number {
  const lastEdge = axis.edges.length - 1;
  const clamped = Math.min(lastEdge, Math.max(0, eventIndex));
  const whole = Math.min(lastEdge - 1, Math.floor(clamped));
  if (whole < 0) return 0;
  const fraction = clamped - whole;
  return axis.edges[whole] + (axis.edges[whole + 1] - axis.edges[whole]) * fraction;
}

/** Maps an x ratio in [0, 1] back to a fractional event index; the inverse of eventIndexToRatio. */
export function ratioToEventIndex(axis: OverviewAxis, ratio: number): number {
  const lastEdge = axis.edges.length - 1;
  if (lastEdge <= 0) return 0;
  const clamped = Math.min(1, Math.max(0, ratio));
  // The last edge whose ratio is at or before the pointer.
  let low = 0;
  let high = lastEdge;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    if (axis.edges[middle] <= clamped) low = middle;
    else high = middle - 1;
  }
  if (low >= lastEdge) return lastEdge;
  const width = axis.edges[low + 1] - axis.edges[low];
  return low + (width > 0 ? (clamped - axis.edges[low]) / width : 0);
}

/** The brush's pixel span for the view window, widened around its center to at least minWidth. */
export function brushPixelSpan(
  axis: OverviewAxis,
  windowStart: number,
  visibleCount: number,
  trackWidth: number,
  minWidth: number,
): { left: number; right: number } {
  let left = eventIndexToRatio(axis, windowStart) * trackWidth;
  let right = eventIndexToRatio(axis, windowStart + visibleCount) * trackWidth;
  const width = Math.min(trackWidth, minWidth);
  if (right - left < width) {
    const center = (left + right) / 2;
    left = Math.min(trackWidth - width, Math.max(0, center - width / 2));
    right = left + width;
  }
  return { left, right };
}
