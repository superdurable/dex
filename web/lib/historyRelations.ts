// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The only reader of optional retained-history and related-event history fields.

import { positiveRunAttempt } from './timeTravel';
import type { FlowHistoryEvent } from './types';

/** An inclusive range of event IDs. */
export interface EventIdRange {
  firstEventId: number;
  lastEventId: number;
}

/** What a history page response reports about the retained history window. */
export interface ReportedRetainedHistory {
  /** The pruned event IDs, or undefined when the Server reports that nothing is pruned. */
  prunedEventIdRange?: EventIdRange;
}

/**
 * Reads the retained-history boundary of a history page response.
 *
 * Returns undefined unless the response carries a positive **firstRetainedEventId** or a valid
 * **prunedEventIdRange**, so zero values from a Server without the fields keep the inference.
 */
export function reportedRetainedHistoryOf(page: unknown): ReportedRetainedHistory | undefined {
  const response = record(page);
  const range = record(response.prunedEventIdRange);
  const firstEventId = eventIdValue(range.startEventId ?? range.start);
  const lastEventId = eventIdValue(range.endEventId ?? range.end);
  if (firstEventId !== undefined && lastEventId !== undefined && firstEventId <= lastEventId) {
    return { prunedEventIdRange: { firstEventId, lastEventId } };
  }
  const firstRetainedEventId = eventIdValue(response.firstRetainedEventId);
  if (firstRetainedEventId === undefined) return undefined;
  // FlowStart (event 1) is always retained, so only a later first event implies a gap.
  return firstRetainedEventId > 2
    ? { prunedEventIdRange: { firstEventId: 2, lastEventId: firstRetainedEventId - 1 } }
    : {};
}

/** The event ID of the WaitFor completion that an Execute event consumed. */
export function waitForCompletedEventIdOf(event: FlowHistoryEvent): number | undefined {
  return eventIdValue(record(event.payload.context).waitForCompletedEventId);
}

/** The time of the WaitFor completion that an Execute event consumed. */
export function waitForCompletedTimeOf(event: FlowHistoryEvent): string | undefined {
  const value = record(event.payload.context).waitForCompletedTime;
  return typeof value === 'string' && value ? value : undefined;
}

/** The event ID of the decision that started the Step execution of its first event. */
export function decidingEventIdOf(event: FlowHistoryEvent): number | undefined {
  return eventIdValue(record(event.payload.context).fromEventId);
}

/**
 * Heuristic for Servers that do not report pruning: server-zd keeps FlowStart and numbers events
 * contiguously, so a gap after FlowStart means pruning.
 *
 * Returns undefined for a gap before a Time Travel fork or another run attempt's event, and for
 * Servers without run attempts, whose event IDs are not contiguous.
 */
export function inferPrunedEventIdRange(oldestEvents: FlowHistoryEvent[]): EventIdRange | undefined {
  const [flowStart, nextEvent] = oldestEvents;
  if (!flowStart || !nextEvent) return undefined;
  if (flowStart.eventId !== 1 || flowStart.type !== 'FlowStartedOrContinued') return undefined;
  const flowStartRunAttempt = positiveRunAttempt(flowStart.runAttempt);
  if (flowStartRunAttempt === 0 || positiveRunAttempt(nextEvent.runAttempt) !== flowStartRunAttempt) {
    return undefined;
  }
  if (nextEvent.type === 'TimeTravelFork' || nextEvent.eventId <= 2) return undefined;
  return { firstEventId: 2, lastEventId: nextEvent.eventId - 1 };
}

export function isEventIdInRange(range: EventIdRange | undefined, eventId: number): boolean {
  return range !== undefined && eventId >= range.firstEventId && eventId <= range.lastEventId;
}

/** protojson sends int64 event IDs as strings; Go structs send numbers. */
function eventIdValue(value: unknown): number | undefined {
  const parsed = typeof value === 'string' && value.trim() !== '' ? Number(value) : value;
  return typeof parsed === 'number' && Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}
