// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { FlowHistoryEvent } from './types';

export interface TimelineStepLink {
  stepExecutionId: string;
  waitForEventId: number;
  executeEventId: number;
  conditionWaitDurationMs: number | null;
  lane: number;
}

export function newestTimelineEvents(events: FlowHistoryEvent[]): FlowHistoryEvent[] {
  return [...events].sort((left, right) => right.eventId - left.eventId);
}

export function buildTimelineStepLinks(events: FlowHistoryEvent[]): TimelineStepLink[] {
  const pendingWaitFor = new Map<string, FlowHistoryEvent>();
  const links: Omit<TimelineStepLink, 'lane'>[] = [];
  const chronologicalEvents = [...events].sort((left, right) => left.eventId - right.eventId);

  for (const event of chronologicalEvents) {
    const stepExecutionId = executionID(event);
    if (!stepExecutionId) continue;
    if (event.type === 'StepWaitForCompleted') {
      pendingWaitFor.set(stepExecutionId, event);
      continue;
    }
    if (
      event.type !== 'StepExecuteCompleted'
      && event.type !== 'StepExecuteFailed'
      && event.type !== 'StepExecutePending'
    ) continue;
    const waitForEvent = pendingWaitFor.get(stepExecutionId);
    if (!waitForEvent) continue;
    links.push({
      stepExecutionId,
      waitForEventId: waitForEvent.eventId,
      executeEventId: event.eventId,
      conditionWaitDurationMs: elapsedMilliseconds(waitForEvent.eventTime, executeStartedTime(event)),
    });
    pendingWaitFor.delete(stepExecutionId);
  }
  return assignTimelineStepLinkLanes(links);
}

export function formatElapsedDuration(milliseconds: number | null): string {
  if (milliseconds === null || !Number.isFinite(milliseconds)) return '';
  const elapsed = Math.max(0, milliseconds);
  if (elapsed < 1000) return `${Math.round(elapsed)}ms`;
  if (elapsed < 10_000) return `${Number((elapsed / 1000).toFixed(1))}s`;
  const seconds = Math.round(elapsed / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
}

function executionID(event: FlowHistoryEvent): string {
  const context = event.payload.context;
  if (!context || typeof context !== 'object') return '';
  const value = (context as Record<string, unknown>).stepExecutionId;
  return typeof value === 'string' ? value : '';
}

function executeStartedTime(event: FlowHistoryEvent): string | null {
  const context = event.payload.context;
  if (context && typeof context === 'object') {
    const value = (context as Record<string, unknown>).startedTime;
    if (typeof value === 'string') return value;
  }
  return event.eventTime;
}

function elapsedMilliseconds(start: string | null, end: string | null): number | null {
  if (!start || !end) return null;
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) return null;
  return Math.max(0, endMs - startMs);
}

function assignTimelineStepLinkLanes(
  links: Omit<TimelineStepLink, 'lane'>[],
): TimelineStepLink[] {
  const laneEndEventIds: number[] = [];
  return [...links]
    .sort((left, right) => left.waitForEventId - right.waitForEventId)
    .map((link) => {
      let lane = laneEndEventIds.findIndex((endEventId) => endEventId < link.waitForEventId);
      if (lane < 0) lane = laneEndEventIds.length;
      laneEndEventIds[lane] = link.executeEventId;
      return { ...link, lane };
    });
}
