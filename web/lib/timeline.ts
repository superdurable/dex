// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  decidingEventIdOf,
  isEventIdInRange,
  waitForCompletedEventIdOf,
  waitForCompletedTimeOf,
  type EventIdRange,
} from './historyRelations';
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

/** Where a related event is: on this page, on another page, removed by the history window, or not known. */
export type TimelineEventLocation = 'loaded' | 'other-page' | 'pruned' | 'unknown';

/** Where a Timeline page sits in its run attempt lineage. */
export interface TimelinePagePosition {
  hasOlder: boolean;
  hasNewer: boolean;
  prunedEventIdRange?: EventIdRange;
}

/** A condition-wait or lineage neighbour of the selected event. */
export interface TimelineRelatedEvent {
  id: string;
  kind: TimelineLink['kind'];
  relation: 'wait-for' | 'execute' | 'source' | 'started';
  /** Whether the related event comes before or after the selected event. */
  direction: 'older' | 'newer';
  stepExecutionId: string;
  /** Names the related event, such as "ReviewStep-3 WaitFor completed". */
  description: string;
  eventId?: number;
  location: TimelineEventLocation;
  elapsedDurationMs: number | null;
}

export interface TimelineSelectedRelations {
  /** Links whose both ends are on the page, with lanes. */
  links: TimelineLink[];
  /** Related events that are not on the page. */
  stubs: TimelineRelatedEvent[];
  related: TimelineRelatedEvent[];
}

export const PRUNED_RELATED_EVENT_EXPLANATION = 'The server keeps only the most recent window of history, '
  + 'so this related event is no longer available.';

type UnassignedTimelineLink = Omit<TimelineLink, 'lane'>;

interface TimelineEventIndex {
  chronological: FlowHistoryEvent[];
  byId: Map<number, FlowHistoryEvent>;
  stepEventsByExecution: Map<string, FlowHistoryEvent[]>;
  decisionsByExecutionAndStepType: Map<string, FlowHistoryEvent[]>;
  rpcCompletionsByName: Map<string, FlowHistoryEvent[]>;
  flowStarts: FlowHistoryEvent[];
  links?: UnassignedTimelineLink[];
}

const stepEventTypes = new Set<FlowHistoryEvent['type']>([
  'StepWaitForCompleted',
  'StepWaitForFailed',
  'StepWaitForPending',
  'StepExecuteCompleted',
  'StepExecuteFailed',
  'StepExecutePending',
]);

const emptyRelations: TimelineSelectedRelations = { links: [], stubs: [], related: [] };
const timelineEventIndexes = new WeakMap<FlowHistoryEvent[], TimelineEventIndex>();

export function newestTimelineEvents(events: FlowHistoryEvent[]): FlowHistoryEvent[] {
  return [...events].sort((left, right) => right.eventId - left.eventId);
}

export function buildTimelineStepLinks(events: FlowHistoryEvent[]): TimelineStepLink[] {
  return assignTimelineStepLinkLanes(pairConditionWaits(timelineEventIndex(events)));
}

export function buildTimelineLinks(events: FlowHistoryEvent[]): TimelineLink[] {
  return assignTimelineLinkLanes(unassignedTimelineLinks(timelineEventIndex(events)));
}

export function buildSelectedTimelineLinks(
  events: FlowHistoryEvent[],
  selectedEventId: number | undefined,
): TimelineLink[] {
  if (selectedEventId === undefined) return [];
  return assignTimelineLinkLanes(
    selectedLinks(unassignedTimelineLinks(timelineEventIndex(events)), selectedEventId),
  );
}

/** Resolves the selected event's related events by ID; one not on the page becomes a stub. */
export function buildSelectedTimelineRelations(
  events: FlowHistoryEvent[],
  selectedEvent: FlowHistoryEvent | null | undefined,
  position: TimelinePagePosition,
): TimelineSelectedRelations {
  if (!selectedEvent) return emptyRelations;
  const index = timelineEventIndex(events);
  const selected = index.byId.get(selectedEvent.eventId);
  if (!selected) return emptyRelations;
  const links = unassignedTimelineLinks(index);
  const related = [
    waitForRelation(index, links, selected, position),
    sourceRelation(index, links, selected, position),
    executeRelation(links, selected, position),
    ...startedRelations(index, links, selected, position),
  ].filter((entry): entry is TimelineRelatedEvent => entry !== undefined);
  return {
    links: assignTimelineLinkLanes(selectedLinks(links, selected.eventId)),
    stubs: related.filter((entry) => entry.location !== 'loaded'),
    related,
  };
}

/** Describes a related event for a stub or a list entry. */
export function timelineRelatedEventLabel(related: TimelineRelatedEvent): string {
  const base = `${related.direction === 'older' ? 'from' : 'to'} ${related.description}`;
  const elapsed = related.elapsedDurationMs === null
    ? ''
    : `, waited ${formatElapsedDuration(related.elapsedDurationMs)}`;
  const page = related.direction === 'older' ? 'older page' : 'newer page';
  switch (related.location) {
    case 'loaded':
      return `${base}, event #${related.eventId}${elapsed}`;
    case 'other-page':
      return `${base}, event #${related.eventId} (${page})${elapsed}`;
    case 'pruned':
      return related.eventId === undefined
        ? `${base} (removed by the history window)${elapsed}`
        : `${base} (event #${related.eventId}, removed by the history window)${elapsed}`;
    default:
      return `${base} (event not on this page)${elapsed}`;
  }
}

/** Explains a pruned related event and keeps its Step execution ID and recorded wait. */
export function prunedRelatedEventTooltip(related: TimelineRelatedEvent): string {
  const execution = related.stepExecutionId ? ` Step execution ID: ${related.stepExecutionId}.` : '';
  const waited = related.elapsedDurationMs === null
    ? ''
    : ` Waited ${formatElapsedDuration(related.elapsedDurationMs)}.`;
  return `${PRUNED_RELATED_EVENT_EXPLANATION}${execution}${waited}`;
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

/** Builds lookup tables in one O(n log n) pass and caches them per events array. */
function timelineEventIndex(events: FlowHistoryEvent[]): TimelineEventIndex {
  const cached = timelineEventIndexes.get(events);
  if (cached) return cached;
  const chronological = [...events].sort((left, right) => left.eventId - right.eventId);
  const index: TimelineEventIndex = {
    chronological,
    byId: new Map(),
    stepEventsByExecution: new Map(),
    decisionsByExecutionAndStepType: new Map(),
    rpcCompletionsByName: new Map(),
    flowStarts: [],
  };
  for (const event of chronological) {
    index.byId.set(event.eventId, event);
    if (event.type === 'FlowStartedOrContinued') index.flowStarts.push(event);
    if (event.type === 'RpcExecutionCompleted') {
      appendTo(index.rpcCompletionsByName, String(event.payload.rpcName || ''), event);
    }
    if (!stepEventTypes.has(event.type)) continue;
    const stepExecutionId = executionID(event);
    if (!stepExecutionId) continue;
    appendTo(index.stepEventsByExecution, stepExecutionId, event);
    for (const nextStepType of new Set(decidedStepTypes(event))) {
      appendTo(index.decisionsByExecutionAndStepType, decisionKey(stepExecutionId, nextStepType), event);
    }
  }
  timelineEventIndexes.set(events, index);
  return index;
}

function unassignedTimelineLinks(index: TimelineEventIndex): UnassignedTimelineLink[] {
  if (index.links) return index.links;
  const conditionLinks = pairConditionWaits(index).map((link) => ({
    kind: 'condition-wait' as const,
    stepExecutionId: link.stepExecutionId,
    fromEventId: link.waitForEventId,
    toEventId: link.executeEventId,
    label: `${link.stepExecutionId}: WaitForCondition started to Execute`,
    elapsedDurationMs: link.conditionWaitDurationMs,
  }));
  index.links = [...lineageLinks(index), ...conditionLinks];
  return index.links;
}

function selectedLinks(links: UnassignedTimelineLink[], selectedEventId: number): UnassignedTimelineLink[] {
  return links.filter((link) => (
    link.kind === 'lineage'
      ? link.toEventId === selectedEventId
      : link.fromEventId === selectedEventId || link.toEventId === selectedEventId
  ));
}

function pairConditionWaits(index: TimelineEventIndex): Omit<TimelineStepLink, 'lane'>[] {
  const pendingWaitFor = new Map<string, FlowHistoryEvent>();
  const links: Omit<TimelineStepLink, 'lane'>[] = [];
  for (const event of index.chronological) {
    const stepExecutionId = executionID(event);
    if (!stepExecutionId) continue;
    if (event.type === 'StepWaitForCompleted') {
      pendingWaitFor.set(stepExecutionId, event);
      continue;
    }
    if (!isExecuteEvent(event)) continue;
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
  return links;
}

function lineageLinks(index: TimelineEventIndex): UnassignedTimelineLink[] {
  const links: UnassignedTimelineLink[] = [];
  for (const [stepExecutionId, stepEvents] of index.stepEventsByExecution) {
    const firstEvent = stepEvents[0];
    if (!isStepExecutionFirstEvent(firstEvent)) continue;
    const source = lineageSourceEvent(index, firstEvent);
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

/** A recorded deciding event ID is exact; the in-page search is the fallback for older events. */
function lineageSourceEvent(index: TimelineEventIndex, target: FlowHistoryEvent): FlowHistoryEvent | undefined {
  const decidingEventId = decidingEventIdOf(target);
  return decidingEventId === undefined ? findLineageSourceEvent(index, target) : index.byId.get(decidingEventId);
}

/** Each lookup is a binary search over one key's events, so this costs O(log n). */
function findLineageSourceEvent(
  index: TimelineEventIndex,
  target: FlowHistoryEvent,
): FlowHistoryEvent | undefined {
  const sourceExecutionId = fromExecutionID(target);
  const flowStart = lastEventBefore(index.flowStarts, target.eventId);
  if (sourceExecutionId === '__start__') return flowStart;
  if (sourceExecutionId.startsWith('__rpc/')) {
    const rpcName = sourceExecutionId.slice('__rpc/'.length);
    return lastEventBefore(index.rpcCompletionsByName.get(rpcName) ?? [], target.eventId);
  }
  const exactSource = lastEventBefore(
    index.decisionsByExecutionAndStepType.get(decisionKey(sourceExecutionId, stepType(target))) ?? [],
    target.eventId,
  );
  if (exactSource) return exactSource;
  const latestSource = lastEventBefore(index.stepEventsByExecution.get(sourceExecutionId) ?? [], target.eventId);
  if (latestSource) return latestSource;
  if (flowStart && hasContinuedStart(flowStart)) return flowStart;
  return undefined;
}

function waitForRelation(
  index: TimelineEventIndex,
  links: UnassignedTimelineLink[],
  selected: FlowHistoryEvent,
  position: TimelinePagePosition,
): TimelineRelatedEvent | undefined {
  const stepExecutionId = executionID(selected);
  if (!stepExecutionId || !isExecuteEvent(selected)) return undefined;
  const base = {
    id: `wait-for-${selected.eventId}`,
    kind: 'condition-wait' as const,
    relation: 'wait-for' as const,
    direction: 'older' as const,
    stepExecutionId,
    description: `${stepExecutionId} WaitFor completed`,
  };
  const loaded = links.find((link) => link.kind === 'condition-wait' && link.toEventId === selected.eventId);
  if (loaded) {
    return { ...base, eventId: loaded.fromEventId, location: 'loaded', elapsedDurationMs: loaded.elapsedDurationMs };
  }
  const conditionResults = record(record(selected.payload.input).conditionResults);
  const reportedEventId = waitForCompletedEventIdOf(selected);
  if (reportedEventId === undefined && (!hasWaitForEvidence(selected) || conditionResults.waitForFailed === true)) {
    return undefined;
  }
  return {
    ...base,
    ...olderFarEnd(index, reportedEventId, position),
    elapsedDurationMs: elapsedMilliseconds(waitForCompletedTimeOf(selected) ?? null, executeStartedTime(selected)),
  };
}

function sourceRelation(
  index: TimelineEventIndex,
  links: UnassignedTimelineLink[],
  selected: FlowHistoryEvent,
  position: TimelinePagePosition,
): TimelineRelatedEvent | undefined {
  const stepExecutionId = executionID(selected);
  if (!stepExecutionId || !stepEventTypes.has(selected.type)) return undefined;
  const base = {
    id: `source-${selected.eventId}`,
    kind: 'lineage' as const,
    relation: 'source' as const,
    direction: 'older' as const,
    stepExecutionId,
    elapsedDurationMs: null,
  };
  const loaded = links.find((link) => link.kind === 'lineage' && link.toEventId === selected.eventId);
  if (loaded) {
    const source = index.byId.get(loaded.fromEventId);
    return {
      ...base,
      description: source ? lineageSourceLabel(source) : fromExecutionID(selected),
      eventId: loaded.fromEventId,
      location: 'loaded',
    };
  }
  const sourceExecutionId = fromExecutionID(selected);
  const firstStepEvent = index.stepEventsByExecution.get(stepExecutionId)?.[0];
  if (!sourceExecutionId || firstStepEvent !== selected || !isStepExecutionFirstEvent(selected)) {
    return undefined;
  }
  // FlowStart is event 1 and is always retained.
  const reportedEventId = decidingEventIdOf(selected) ?? (sourceExecutionId === '__start__' ? 1 : undefined);
  return {
    ...base,
    description: sourceExecutionLabel(sourceExecutionId),
    ...olderFarEnd(index, reportedEventId, position),
  };
}

function executeRelation(
  links: UnassignedTimelineLink[],
  selected: FlowHistoryEvent,
  position: TimelinePagePosition,
): TimelineRelatedEvent | undefined {
  const stepExecutionId = executionID(selected);
  if (!stepExecutionId || selected.type !== 'StepWaitForCompleted') return undefined;
  const base = {
    id: `execute-${selected.eventId}`,
    kind: 'condition-wait' as const,
    relation: 'execute' as const,
    direction: 'newer' as const,
    stepExecutionId,
    description: `${stepExecutionId} Execute`,
  };
  const loaded = links.find((link) => link.kind === 'condition-wait' && link.fromEventId === selected.eventId);
  if (loaded) {
    return { ...base, eventId: loaded.toEventId, location: 'loaded', elapsedDurationMs: loaded.elapsedDurationMs };
  }
  // On the newest page, a missing Execute has not been recorded yet.
  return position.hasNewer ? { ...base, location: 'unknown', elapsedDurationMs: null } : undefined;
}

function startedRelations(
  index: TimelineEventIndex,
  links: UnassignedTimelineLink[],
  selected: FlowHistoryEvent,
  position: TimelinePagePosition,
): TimelineRelatedEvent[] {
  const started: TimelineRelatedEvent[] = links
    .filter((link) => link.kind === 'lineage' && link.fromEventId === selected.eventId)
    .map((link) => ({
      id: `started-${link.toEventId}`,
      kind: 'lineage',
      relation: 'started',
      direction: 'newer',
      stepExecutionId: link.stepExecutionId,
      description: `${link.stepExecutionId} first event`,
      eventId: link.toEventId,
      location: 'loaded',
      elapsedDurationMs: null,
    }));
  if (!position.hasNewer) return started;
  const unmatchedStepTypes = decidedStepTypes(selected);
  for (const link of started) {
    const target = index.byId.get(link.eventId ?? 0);
    const matched = target ? unmatchedStepTypes.indexOf(stepType(target)) : -1;
    if (matched >= 0) unmatchedStepTypes.splice(matched, 1);
  }
  return [
    ...started,
    ...unmatchedStepTypes.map((nextStepType, offset) => ({
      id: `started-${selected.eventId}-${offset}`,
      kind: 'lineage' as const,
      relation: 'started' as const,
      direction: 'newer' as const,
      stepExecutionId: '',
      description: `${nextStepType} execution`,
      location: 'unknown' as const,
      elapsedDurationMs: null,
    })),
  ];
}

/** Classifies an older related event by ID, or by page position when its ID is unknown. */
function olderFarEnd(
  index: TimelineEventIndex,
  eventId: number | undefined,
  position: TimelinePagePosition,
): Pick<TimelineRelatedEvent, 'eventId' | 'location'> {
  if (eventId !== undefined) {
    if (index.byId.has(eventId)) return { eventId, location: 'loaded' };
    if (isEventIdInRange(position.prunedEventIdRange, eventId)) return { eventId, location: 'pruned' };
    return { eventId, location: 'other-page' };
  }
  // The oldest page holds every retained older event, so a missing one was pruned.
  if (!position.hasOlder && position.prunedEventIdRange) return { location: 'pruned' };
  return { location: 'unknown' };
}

/** Execute input carries condition results only when the Step execution ran WaitFor. */
function hasWaitForEvidence(event: FlowHistoryEvent): boolean {
  return isExecuteEvent(event) && (
    Object.hasOwn(record(event.payload.input), 'conditionResults')
    || waitForCompletedEventIdOf(event) !== undefined
  );
}

function isStepExecutionFirstEvent(event: FlowHistoryEvent): boolean {
  if (event.type.startsWith('StepWaitFor')) return true;
  return isExecuteEvent(event) && !hasWaitForEvidence(event);
}

function isExecuteEvent(event: FlowHistoryEvent): boolean {
  return event.type === 'StepExecuteCompleted'
    || event.type === 'StepExecuteFailed'
    || event.type === 'StepExecutePending';
}

/** The Step types a decision event starts. */
function decidedStepTypes(event: FlowHistoryEvent): string[] {
  if (event.type === 'FlowStartedOrContinued') {
    const startStepType = record(event.payload.initialStart).startStepType;
    return typeof startStepType === 'string' && startStepType ? [startStepType] : [];
  }
  const decision = event.type === 'RpcExecutionCompleted'
    ? record(event.payload.stepDecision)
    : record(record(event.payload.output).stepDecision);
  const nextSteps = Array.isArray(decision.nextSteps) ? decision.nextSteps : [];
  return nextSteps
    .map((movement) => record(movement).stepType)
    .filter((value): value is string => typeof value === 'string' && value !== '');
}

function decisionKey(stepExecutionId: string, nextStepType: string): string {
  return `${stepExecutionId}\n${nextStepType}`;
}

function executionID(event: FlowHistoryEvent): string {
  const value = record(event.payload.context).stepExecutionId;
  return typeof value === 'string' ? value : '';
}

function fromExecutionID(event: FlowHistoryEvent): string {
  const value = record(event.payload.context).fromStepExecutionId;
  return typeof value === 'string' ? value : '';
}

function stepType(event: FlowHistoryEvent): string {
  const value = record(event.payload.context).stepType;
  return typeof value === 'string' ? value : '';
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

function sourceExecutionLabel(sourceExecutionId: string): string {
  if (sourceExecutionId === '__start__') return 'Flow start';
  if (sourceExecutionId.startsWith('__rpc/')) return `RPC ${sourceExecutionId.slice('__rpc/'.length)}`;
  return `${sourceExecutionId} decision`;
}

function hasContinuedStart(event: FlowHistoryEvent): boolean {
  return Object.keys(record(event.payload.continuedStart)).length > 0;
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function appendTo<K>(map: Map<K, FlowHistoryEvent[]>, key: K, event: FlowHistoryEvent) {
  const values = map.get(key);
  if (values) values.push(event);
  else map.set(key, [event]);
}

/** Returns the newest event below an event ID from events in ascending order. */
function lastEventBefore(events: FlowHistoryEvent[], eventId: number): FlowHistoryEvent | undefined {
  let low = 0;
  let high = events.length;
  while (low < high) {
    const middle = (low + high) >> 1;
    if (events[middle].eventId < eventId) low = middle + 1;
    else high = middle;
  }
  return low > 0 ? events[low - 1] : undefined;
}

function executeStartedTime(event: FlowHistoryEvent): string | null {
  const value = record(event.payload.context).startedTime;
  return typeof value === 'string' ? value : event.eventTime;
}

function elapsedMilliseconds(start: string | null, end: string | null): number | null {
  if (!start || !end) return null;
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) return null;
  return Math.max(0, endMs - startMs);
}

function assignTimelineStepLinkLanes(links: Omit<TimelineStepLink, 'lane'>[]): TimelineStepLink[] {
  const sorted = [...links].sort((left, right) => left.waitForEventId - right.waitForEventId);
  const lanes = assignLowestFreeLanes(
    sorted.map((link) => [link.waitForEventId, link.executeEventId]),
    (laneEndEventId, startEventId) => laneEndEventId < startEventId,
  );
  return sorted.map((link, position) => ({ ...link, lane: lanes[position] }));
}

function assignTimelineLinkLanes(links: UnassignedTimelineLink[]): TimelineLink[] {
  const sorted = [...links].sort((left, right) => (
    left.fromEventId - right.fromEventId || left.toEventId - right.toEventId
  ));
  const lanes = assignLowestFreeLanes(
    sorted.map((link) => [link.fromEventId, link.toEventId]),
    (laneEndEventId, startEventId) => laneEndEventId <= startEventId,
  );
  return sorted.map((link, position) => ({ ...link, lane: lanes[position] }));
}

/**
 * Gives each interval, sorted by start, the lowest lane it may follow; two heaps keep this O(n log n).
 *
 * Returns one lane per interval.
 */
function assignLowestFreeLanes(
  intervals: Array<[startEventId: number, endEventId: number]>,
  canFollow: (laneEndEventId: number, startEventId: number) => boolean,
): number[] {
  const busyLanes = new MinHeap<[endEventId: number, lane: number]>(
    (left, right) => left[0] - right[0] || left[1] - right[1],
  );
  const freeLanes = new MinHeap<number>((left, right) => left - right);
  let laneCount = 0;
  return intervals.map(([startEventId, endEventId]) => {
    while (busyLanes.size > 0 && canFollow(busyLanes.peek()[0], startEventId)) {
      freeLanes.push(busyLanes.pop()[1]);
    }
    const lane = freeLanes.size > 0 ? freeLanes.pop() : laneCount++;
    busyLanes.push([endEventId, lane]);
    return lane;
  });
}

class MinHeap<T> {
  private readonly values: T[] = [];
  private readonly compare: (left: T, right: T) => number;

  constructor(compare: (left: T, right: T) => number) {
    this.compare = compare;
  }

  get size(): number {
    return this.values.length;
  }

  peek(): T {
    return this.values[0];
  }

  push(value: T) {
    this.values.push(value);
    let child = this.values.length - 1;
    while (child > 0) {
      const parent = (child - 1) >> 1;
      if (this.compare(this.values[parent], this.values[child]) <= 0) break;
      [this.values[parent], this.values[child]] = [this.values[child], this.values[parent]];
      child = parent;
    }
  }

  pop(): T {
    const top = this.values[0];
    const last = this.values.pop() as T;
    if (this.values.length === 0) return top;
    this.values[0] = last;
    let parent = 0;
    for (;;) {
      const left = parent * 2 + 1;
      const right = left + 1;
      let smallest = parent;
      if (left < this.values.length && this.compare(this.values[left], this.values[smallest]) < 0) smallest = left;
      if (right < this.values.length && this.compare(this.values[right], this.values[smallest]) < 0) smallest = right;
      if (smallest === parent) return top;
      [this.values[parent], this.values[smallest]] = [this.values[smallest], this.values[parent]];
      parent = smallest;
    }
  }
}
