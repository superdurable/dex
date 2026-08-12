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

export interface TimelineLink {
  kind: 'lineage' | 'condition-wait';
  stepExecutionId: string;
  fromEventId: number;
  toEventId: number;
  label: string;
  elapsedDurationMs: number | null;
  lane: number;
}

const stepEventTypes = new Set<FlowHistoryEvent['type']>([
  'StepWaitForCompleted',
  'StepWaitForFailed',
  'StepWaitForPending',
  'StepExecuteCompleted',
  'StepExecuteFailed',
  'StepExecutePending',
]);

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

export function buildTimelineLinks(events: FlowHistoryEvent[]): TimelineLink[] {
  return assignTimelineLinkLanes(buildUnassignedTimelineLinks(events));
}

export function buildSelectedTimelineLinks(
  events: FlowHistoryEvent[],
  selectedEventId: number | undefined,
): TimelineLink[] {
  if (selectedEventId === undefined) return [];
  const selectedLinks = buildUnassignedTimelineLinks(events).filter((link) => (
    link.kind === 'lineage'
      ? link.toEventId === selectedEventId
      : link.fromEventId === selectedEventId || link.toEventId === selectedEventId
  ));
  return assignTimelineLinkLanes(selectedLinks);
}

function buildUnassignedTimelineLinks(
  events: FlowHistoryEvent[],
): Omit<TimelineLink, 'lane'>[] {
  const conditionLinks = buildTimelineStepLinks(events).map((link) => ({
    kind: 'condition-wait' as const,
    stepExecutionId: link.stepExecutionId,
    fromEventId: link.waitForEventId,
    toEventId: link.executeEventId,
    label: `${link.stepExecutionId}: WaitForCondition started to Execute`,
    elapsedDurationMs: link.conditionWaitDurationMs,
  }));
  const lineageLinks = buildTimelineLineageLinks(events);
  return [...lineageLinks, ...conditionLinks];
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

function fromExecutionID(event: FlowHistoryEvent): string {
  const context = event.payload.context;
  if (!context || typeof context !== 'object') return '';
  const value = (context as Record<string, unknown>).fromStepExecutionId;
  return typeof value === 'string' ? value : '';
}

function stepType(event: FlowHistoryEvent): string {
  const context = event.payload.context;
  if (!context || typeof context !== 'object') return '';
  const value = (context as Record<string, unknown>).stepType;
  return typeof value === 'string' ? value : '';
}

function buildTimelineLineageLinks(
  events: FlowHistoryEvent[],
): Omit<TimelineLink, 'lane'>[] {
  const chronologicalEvents = [...events].sort((left, right) => left.eventId - right.eventId);
  const firstStepEvents = new Map<string, FlowHistoryEvent>();
  for (const event of chronologicalEvents) {
    if (!stepEventTypes.has(event.type)) continue;
    const stepExecutionId = executionID(event);
    if (stepExecutionId && !firstStepEvents.has(stepExecutionId)) {
      firstStepEvents.set(stepExecutionId, event);
    }
  }

  const links: Omit<TimelineLink, 'lane'>[] = [];
  for (const [stepExecutionId, firstEvent] of firstStepEvents) {
    const source = findLineageSourceEvent(chronologicalEvents, firstEvent);
    if (!source || source.eventId >= firstEvent.eventId) continue;
    links.push({
      kind: 'lineage',
      stepExecutionId,
      fromEventId: source.eventId,
      toEventId: firstEvent.eventId,
      label: `${lineageSourceLabel(source)} to ${stepExecutionId} first event`,
      elapsedDurationMs: null,
    });
  }
  return links;
}

function findLineageSourceEvent(
  chronologicalEvents: FlowHistoryEvent[],
  target: FlowHistoryEvent,
): FlowHistoryEvent | undefined {
  const priorEvents = chronologicalEvents.filter((event) => event.eventId < target.eventId);
  const sourceExecutionId = fromExecutionID(target);
  const flowStart = priorEvents.findLast((event) => event.type === 'FlowStartedOrContinued');
  if (sourceExecutionId === '__start__') return flowStart;
  if (sourceExecutionId.startsWith('__rpc/')) {
    const rpcName = sourceExecutionId.slice('__rpc/'.length);
    return priorEvents.findLast((event) => (
      event.type === 'RpcExecutionCompleted' && event.payload.rpcName === rpcName
    ));
  }

  const sourceStepEvents = priorEvents.filter((event) => executionID(event) === sourceExecutionId);
  const exactSource = sourceStepEvents.findLast((event) => producesStepType(event, stepType(target)));
  if (exactSource) return exactSource;
  if (sourceStepEvents.length > 0) return sourceStepEvents.at(-1);
  if (flowStart && hasContinuedStart(flowStart)) return flowStart;
  return undefined;
}

function producesStepType(event: FlowHistoryEvent, targetStepType: string): boolean {
  const output = record(event.payload.output);
  const stepDecision = record(output.stepDecision);
  const nextSteps = Array.isArray(stepDecision.nextSteps) ? stepDecision.nextSteps : [];
  return nextSteps.some((movement) => record(movement).stepType === targetStepType);
}

function lineageSourceLabel(event: FlowHistoryEvent): string {
  if (event.type === 'FlowStartedOrContinued') {
    return hasContinuedStart(event) ? 'Flow continued' : 'Flow start';
  }
  if (event.type === 'RpcExecutionCompleted') {
    return `RPC ${String(event.payload.rpcName || '')}`.trim();
  }
  const stepExecutionId = executionID(event);
  if (event.type === 'StepExecuteFailed') return `${stepExecutionId} failure recovery`;
  return `${stepExecutionId} decision`;
}

function hasContinuedStart(event: FlowHistoryEvent): boolean {
  return Object.keys(record(event.payload.continuedStart)).length > 0;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
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

function assignTimelineLinkLanes(links: Omit<TimelineLink, 'lane'>[]): TimelineLink[] {
  const laneEndEventIds: number[] = [];
  return [...links]
    .sort((left, right) => (
      left.fromEventId - right.fromEventId || left.toEventId - right.toEventId
    ))
    .map((link) => {
      let lane = laneEndEventIds.findIndex((endEventId) => endEventId <= link.fromEventId);
      if (lane < 0) lane = laneEndEventIds.length;
      laneEndEventIds[lane] = link.toEventId;
      return { ...link, lane };
    });
}
