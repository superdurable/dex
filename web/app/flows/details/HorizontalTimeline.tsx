// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import { formatDate, type TimezonePreference } from '@/lib/format';
import type { EventIdRange } from '@/lib/historyRelations';
import {
  buildSelectedTimelineRelations,
  formatElapsedDuration,
  type TimelineSelectedRelations,
} from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  Flag,
  FlagOff,
  GitFork,
  Hourglass,
  Play,
  RotateCcw,
  Send,
  Tag,
  Trash2,
  X,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { flowStatusLabel } from '@/lib/semantic';
import { useTimezonePreference } from '../../providers';
import { eventTitle } from './EventDetails';
import { executionSummary, type TimelinePaging } from './Timeline';
import {
  brushPixelSpan,
  bucketOverviewEvents,
  buildOverviewAxis,
  hasRetriedFailure,
  minimumPositionGap,
  ratioToEventIndex,
  type EventStatus,
  type OverviewAxis,
  type OverviewBucket,
} from './timelineOverview';

/** The one knob for the label slant; spacing, the label run, and the strip height follow from it. */
const labelAngleDegrees = 20;
const labelMaxWidth = 240;
/** Space between neighboring slanted labels, measured across the slant. */
const labelGap = 24;
const labelAngle = (labelAngleDegrees * Math.PI) / 180;

/** The strip geometry the label angle implies. */
export const horizontalTimelineLayout = {
  labelAngleDegrees,
  labelMaxWidth,
  /** Horizontal space per event, so slanted neighbors stay labelGap apart. */
  slotWidth: Math.ceil(labelGap / Math.sin(labelAngle)),
  /** Room at the right edge for the labels of the newest visible events. */
  labelRunWidth: Math.ceil(labelMaxWidth * Math.cos(labelAngle)) + 10,
  /** Dots, axis, and arcs take 92 px; the labels rise above them. */
  stripHeight: 92 + Math.ceil(labelMaxWidth * Math.sin(labelAngle)) + 24,
};
const { slotWidth, labelRunWidth } = horizontalTimelineLayout;
const overscanSlots = 6;
const fallbackStripWidth = 1200;
const arcBandHeight = 44;
/** The overview histogram: a marker lane for problems above the bars, about one bar per 7 px. */
const overviewMarkerLaneHeight = 9;
const overviewBarAreaHeight = 50;
const overviewHeight = overviewMarkerLaneHeight + overviewBarAreaHeight;
const overviewBucketWidth = 7;
const overviewMinBarHeight = 3;
/** A failed, waiting, or canceled share never shrinks below this, however many normal events share its bar. */
const overviewStatusCapHeight = 5;
const overviewMinBrushWidth = 24;
/** Room at both ends, so the first and last bars clear the brush handles. */
const overviewPlotInset = 10;
/** On a short page each event gets its own bar; time spacing must leave neighbors this many pixels apart. */
const overviewFewEventMinGap = 10;
const overviewFewEventMaxBarWidth = 20;

type EventKind = 'waitfor' | 'execute' | 'channel-publish' | 'channel-delete' | 'flow-start' | 'flow-close'
  | 'rpc' | 'attributes' | 'fork';

type KindFamily = 'step' | 'input' | 'lifecycle';

/** Each event kind's icon, legend name, and family; the family sets a quiet icon tint. */
const eventKinds: Record<EventKind, { icon: LucideIcon; name: string; family: KindFamily }> = {
  waitfor: { icon: Hourglass, name: 'WaitFor', family: 'step' },
  execute: { icon: Play, name: 'Execute', family: 'step' },
  'channel-publish': { icon: Send, name: 'Channel published', family: 'input' },
  'channel-delete': { icon: Trash2, name: 'Channel message deleted', family: 'input' },
  rpc: { icon: Zap, name: 'RPC', family: 'input' },
  attributes: { icon: Tag, name: 'Attributes updated', family: 'input' },
  'flow-start': { icon: Flag, name: 'Flow started', family: 'lifecycle' },
  'flow-close': { icon: FlagOff, name: 'Flow closed', family: 'lifecycle' },
  fork: { icon: GitFork, name: 'Time Travel fork', family: 'lifecycle' },
};

function eventKindOf(event: FlowHistoryEvent): EventKind {
  if (event.type.startsWith('StepWaitFor')) return 'waitfor';
  if (event.type.startsWith('StepExecute')) return 'execute';
  if (event.type === 'ChannelExternalPublish') return 'channel-publish';
  if (event.type === 'ChannelExternalDelete') return 'channel-delete';
  if (event.type === 'FlowStartedOrContinued') return 'flow-start';
  if (event.type === 'FlowClosed') return 'flow-close';
  if (event.type === 'TimeTravelFork') return 'fork';
  return event.payload.isSetAttributeApi === true ? 'attributes' : 'rpc';
}

const statusNames: Record<Exclude<EventStatus, 'normal'>, string> = {
  waiting: 'Waiting',
  failed: 'Failed',
  canceled: 'Canceled',
};

function contextOf(event: FlowHistoryEvent): Record<string, unknown> {
  const context = event.payload.context;
  return context && typeof context === 'object' ? context as Record<string, unknown> : {};
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  return typeof value === 'string' ? value : '';
}

function stringList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string') : [];
}

/** An event's derived status, and how its accessible name states it. */
interface EventState {
  status: EventStatus;
  /** Spoken after the subject, such as still waiting or canceled; empty when the title says it. */
  spoken: string;
}

interface OpenWait {
  eventId: number;
  stepType: string;
  fromStepExecutionId: string;
}

const normalState: EventState = { status: 'normal', spoken: '' };
const canceledState: EventState = { status: 'canceled', spoken: 'canceled' };

/** A wait lasts until its Execute starts; cancel decisions, Flow close, and close-time Pending records cancel. */
function eventStates(chronological: FlowHistoryEvent[], hasNewer: boolean): Map<number, EventState> {
  const states = new Map<number, EventState>();
  const openWaits = new Map<string, OpenWait>();
  const cancelOpenWaits = (isTarget: (wait: OpenWait) => boolean) => {
    for (const [stepExecutionId, wait] of openWaits) {
      if (!isTarget(wait)) continue;
      states.set(wait.eventId, canceledState);
      openWaits.delete(stepExecutionId);
    }
  };
  for (const event of chronological) {
    const context = contextOf(event);
    const stepExecutionId = stringField(context, 'stepExecutionId');
    if (event.type.endsWith('Failed')) states.set(event.eventId, { status: 'failed', spoken: '' });
    else if (event.type.endsWith('Pending')) states.set(event.eventId, { status: 'canceled', spoken: 'interrupted by the Flow close' });
    else states.set(event.eventId, normalState);

    if (event.type === 'StepWaitForCompleted' && stepExecutionId) {
      openWaits.set(stepExecutionId, {
        eventId: event.eventId,
        stepType: stringField(context, 'stepType'),
        fromStepExecutionId: stringField(context, 'fromStepExecutionId'),
      });
    }
    if (event.type.startsWith('StepExecute') && stepExecutionId) {
      const wait = openWaits.get(stepExecutionId);
      if (wait) states.set(wait.eventId, { status: 'normal', spoken: 'conditions met' });
      openWaits.delete(stepExecutionId);
    }
    const output = event.payload.output && typeof event.payload.output === 'object'
      ? event.payload.output as Record<string, unknown>
      : {};
    const decision = (event.type === 'StepExecuteCompleted' ? output.stepDecision : event.payload.stepDecision);
    if (decision && typeof decision === 'object') {
      const flowWide = stringList((decision as Record<string, unknown>).cancelStepTypes);
      // Sibling selectors match Steps of the deciding Step's parent; RPC decisions have none.
      const siblings = event.type === 'StepExecuteCompleted'
        ? stringList((decision as Record<string, unknown>).cancelSiblingStepTypes)
        : [];
      const parent = stringField(context, 'fromStepExecutionId');
      cancelOpenWaits((wait) => flowWide.includes(wait.stepType)
        || (siblings.includes(wait.stepType) && wait.fromStepExecutionId === parent));
    }
    if (event.type === 'FlowClosed') {
      const flowStatus = flowStatusLabel(event.payload.flowStatus);
      if (flowStatus === 'Failed' || flowStatus.includes('timeout')) states.set(event.eventId, { status: 'failed', spoken: flowStatus.toLowerCase() });
      if (flowStatus === 'Canceled' || flowStatus === 'Terminated') states.set(event.eventId, { status: 'canceled', spoken: flowStatus.toLowerCase() });
      cancelOpenWaits(() => true);
    }
  }
  if (!hasNewer) {
    for (const wait of openWaits.values()) states.set(wait.eventId, { status: 'waiting', spoken: 'still waiting' });
  }
  return states;
}

function namesIn(values: unknown, field: string): string[] {
  const list = Array.isArray(values) ? values : [];
  return [...new Set(list.flatMap((value) => {
    const name = value && typeof value === 'object' ? (value as Record<string, unknown>)[field] : undefined;
    return typeof name === 'string' && name ? [name] : [];
  }))];
}

/** The axis names every tenth event, so the slanted labels need no numbers. */
const axisEventInterval = 10;

/** Names what an event is about, such as its Step type or Channel name; the icon names its kind. */
export function eventSubject(event: FlowHistoryEvent): string {
  const kind = eventKindOf(event);
  if (kind === 'waitfor' || kind === 'execute') return executionSummary(event).split(' · ')[0] || eventKinds[kind].name;
  if (kind === 'channel-publish' || kind === 'channel-delete') {
    return namesIn(event.payload.messages, 'channelName').join(', ') || eventKinds[kind].name;
  }
  if (kind === 'rpc' && typeof event.payload.rpcName === 'string' && event.payload.rpcName) return event.payload.rpcName;
  if (kind === 'attributes') return namesIn(event.payload.upsertAttributes, 'key').join(', ') || eventKinds[kind].name;
  return eventTitle(event);
}

function EventKindLegend() {
  return (
    <div className="htl-legend">
      <ul aria-label="Event kinds">
        {(['waitfor', 'execute', 'channel-publish', 'channel-delete', 'rpc', 'attributes', 'flow-start', 'flow-close', 'fork'] as EventKind[]).map((kind) => {
          const Icon = eventKinds[kind].icon;
          return (
            <li key={kind}>
              <span aria-hidden="true" className={`htl-legend-mark htl-family-${eventKinds[kind].family}`}>
                <Icon size={13} strokeWidth={2.25} />
              </span>
              {eventKinds[kind].name}
            </li>
          );
        })}
      </ul>
      <ul aria-label="Status colors">
        {(['waiting', 'failed', 'canceled'] as const).map((status) => (
          <li key={status}>
            <span aria-hidden="true" className="htl-legend-mark">
              <span className={`htl-legend-dot htl-status-${status}`}>
                {status === 'failed' && <X size={8} strokeWidth={3.5} />}
              </span>
            </span>
            {statusNames[status]}
          </li>
        ))}
        <li>
          <span aria-hidden="true" className="htl-legend-mark">
            <span className="htl-legend-dot htl-legend-retried"><RotateCcw size={8} strokeWidth={3.5} /></span>
          </span>
          Retried after failure
        </li>
      </ul>
    </div>
  );
}

/** How a retried Step event states its failed attempts after the subject. */
function retriedSpoken(event: FlowHistoryEvent): string {
  const finalAttempt = Number(contextOf(event).finalAttempt);
  return Number.isFinite(finalAttempt) && finalAttempt > 1
    ? `succeeded on attempt ${finalAttempt}`
    : 'succeeded after a failed attempt';
}

const clockFormatters = new Map<string, Intl.DateTimeFormat>();

/** Time of day with seconds, plus tenths when a range is shorter than a minute. */
function formatClock(epochMs: number, timezone: TimezonePreference, showsTenths: boolean): string {
  const key = `${timezone}-${showsTenths}`;
  let formatter = clockFormatters.get(key);
  if (!formatter) {
    formatter = new Intl.DateTimeFormat(undefined, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      fractionalSecondDigits: showsTenths ? 1 : undefined,
      timeZone: timezone === 'UTC' ? 'UTC' : undefined,
    });
    clockFormatters.set(key, formatter);
  }
  return formatter.format(epochMs);
}

/** Labels the two ends of the overview axis with clock times, led by event IDs when bars follow event order. */
function overviewAxisEnds(
  axis: OverviewAxis,
  events: readonly FlowHistoryEvent[],
  timezone: TimezonePreference,
): [string, string] {
  const firstId = `#${events[0].eventId}`;
  const lastId = `#${events[events.length - 1].eventId}`;
  if (axis.startMs === null || axis.endMs === null) return [firstId, lastId];
  let ends: [string, string];
  const isSameDay = new Date(axis.startMs).toDateString() === new Date(axis.endMs).toDateString();
  if (!isSameDay) {
    ends = [formatDate(new Date(axis.startMs).toISOString(), timezone), formatDate(new Date(axis.endMs).toISOString(), timezone)];
  } else {
    const showsTenths = axis.endMs - axis.startMs < 60_000;
    ends = [formatClock(axis.startMs, timezone, showsTenths), formatClock(axis.endMs, timezone, showsTenths)];
  }
  return axis.kind === 'time' ? ends : [`${firstId} · ${ends[0]}`, `${ends[1]} · ${lastId}`];
}

/** The clock range a bar covers: its slice of the time axis, or its own events' times. */
function overviewBucketTimeRange(
  axis: OverviewAxis,
  bucket: OverviewBucket,
  bucketIndex: number,
  bucketCount: number,
  events: readonly FlowHistoryEvent[],
  timezone: TimezonePreference,
): string | null {
  if (axis.startMs === null || axis.endMs === null) return null;
  const span = axis.endMs - axis.startMs;
  let from = axis.startMs + (span * bucketIndex) / bucketCount;
  let to = axis.startMs + (span * (bucketIndex + 1)) / bucketCount;
  if (axis.kind === 'index' || bucket.count === 1) {
    from = Date.parse(events[bucket.firstEventIndex].eventTime ?? '');
    to = Date.parse(events[bucket.lastEventIndex].eventTime ?? '');
    if (!Number.isFinite(from) || !Number.isFinite(to)) return null;
  }
  const showsTenths = to - from < 60_000;
  return from === to
    ? formatClock(from, timezone, showsTenths)
    : `${formatClock(from, timezone, showsTenths)} – ${formatClock(to, timezone, showsTenths)}`;
}

/** The hover card lines for one bar: its range, its event count, and its problems. */
function overviewBucketLines(
  axis: OverviewAxis,
  bucket: OverviewBucket,
  bucketIndex: number,
  bucketCount: number,
  events: readonly FlowHistoryEvent[],
  timezone: TimezonePreference,
): { range: string; detail: string; problems: { tone: Exclude<EventStatus, 'normal'>; text: string }[] } {
  const firstId = events[bucket.firstEventIndex].eventId;
  const lastId = events[bucket.lastEventIndex].eventId;
  const ids = firstId === lastId ? `#${firstId}` : `#${firstId} – #${lastId}`;
  const eventCount = `${bucket.count} ${bucket.count === 1 ? 'event' : 'events'}`;
  const timeRange = overviewBucketTimeRange(axis, bucket, bucketIndex, bucketCount, events, timezone);
  const range = timeRange ?? `Events ${ids}`;
  let detail = timeRange ? `${eventCount} · ${ids}` : eventCount;
  if (bucket.count === 1) detail = `${ids} · ${eventSubject(events[bucket.firstEventIndex])}`;
  const problems: { tone: Exclude<EventStatus, 'normal'>; text: string }[] = [];
  if (bucket.failed) problems.push({ tone: 'failed', text: `${bucket.failed} failed` });
  if (bucket.retried) {
    problems.push({ tone: 'failed', text: `${bucket.retried} retried after ${bucket.retried === 1 ? 'a failed attempt' : 'failed attempts'}` });
  }
  if (bucket.waiting) problems.push({ tone: 'waiting', text: `${bucket.waiting} waiting` });
  if (bucket.canceled) problems.push({ tone: 'canceled', text: `${bucket.canceled} canceled` });
  return { range, detail, problems };
}

/** One bar's stacked segments from the top: failures, waits, cancels, then normal events. */
function overviewBarSegments(bucket: OverviewBucket, maxCount: number): { tone: EventStatus; height: number }[] {
  const scaled = Math.round((bucket.count / Math.max(1, maxCount)) * (overviewBarAreaHeight - 2));
  let height = Math.max(overviewMinBarHeight, scaled);
  const shares: [Exclude<EventStatus, 'normal'>, number][] = [
    ['failed', bucket.failed + bucket.retried],
    ['waiting', bucket.waiting],
    ['canceled', bucket.canceled],
  ];
  const segments = shares
    .filter(([, count]) => count > 0)
    .map(([tone, count]) => ({ tone, height: Math.max(overviewStatusCapHeight, Math.round((count / bucket.count) * height)) }));
  const statusHeight = segments.reduce((sum, segment) => sum + segment.height, 0);
  const normalCount = bucket.count - shares.reduce((sum, [, count]) => sum + count, 0);
  if (normalCount > 0) height = Math.max(height, statusHeight + 2);
  const remaining = Math.min(overviewBarAreaHeight, height) - statusHeight;
  return remaining > 0 && normalCount > 0 ? [...segments, { tone: 'normal', height: remaining }] : segments;
}

/** Shows a Timeline page left to right with slanted labels; only events in view render. */
export function HorizontalTimeline({
  events,
  selectedEvent,
  onSelectEvent,
  relations,
  paging,
  prunedEventIdRange,
  highlightedEventId = null,
  isFollowingLatest = false,
}: {
  events: FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
  relations?: TimelineSelectedRelations;
  paging?: TimelinePaging;
  prunedEventIdRange?: EventIdRange;
  highlightedEventId?: number | null;
  /** The selection is the newest event by default, so Live moving it must not move the view. */
  isFollowingLatest?: boolean;
}) {
  const chronological = useMemo(() => [...events].sort((left, right) => left.eventId - right.eventId), [events]);
  const states = useMemo(() => eventStates(chronological, paging?.hasNewer ?? false), [chronological, paging?.hasNewer]);
  const indexByEventId = useMemo(
    () => new Map(chronological.map((event, index) => [event.eventId, index])),
    [chronological],
  );
  const stripRef = useRef<HTMLDivElement>(null);
  const helpId = useId();
  const minimapRef = useRef<HTMLDivElement>(null);
  const [stripWidth, setStripWidth] = useState(fallbackStripWidth);
  const visibleCount = Math.max(1, Math.floor((stripWidth - labelRunWidth) / slotWidth));
  const maxStart = Math.max(0, chronological.length - visibleCount);
  const [windowStart, setWindowStart] = useState(maxStart);
  const isAtNewestEnd = useRef(true);
  const isDraggingMinimap = useRef(false);
  /** Where the drag grabbed the brush, in events from its left edge, so dragging never jumps. */
  const brushGrabOffset = useRef(0);
  const [hoveredBucket, setHoveredBucket] = useState<number | null>(null);
  const [isPointerOverBrush, setIsPointerOverBrush] = useState(false);
  const timezone = useTimezonePreference();
  const plotWidth = Math.max(1, stripWidth - 2 * overviewPlotInset);
  const histogramBucketCount = Math.max(1, Math.floor(plotWidth / overviewBucketWidth));
  // A page with fewer events than bars draws one wide bar per event instead of slivers.
  const isFewEvents = chronological.length <= histogramBucketCount;
  const overviewAxis = useMemo(
    () => buildOverviewAxis(chronological, histogramBucketCount, isFewEvents ? overviewFewEventMinGap / plotWidth : 0),
    [chronological, histogramBucketCount, isFewEvents, plotWidth],
  );
  const bucketCount = isFewEvents && overviewAxis.kind === 'index' ? Math.max(1, chronological.length) : histogramBucketCount;
  /** Each event's own bar width on a short page: as wide as its neighbors allow, up to the cap. */
  const fewEventBarWidth = isFewEvents
    ? Math.max(3, Math.min(overviewFewEventMaxBarWidth, minimumPositionGap(overviewAxis) * plotWidth - 3))
    : 0;
  const retriedFlags = useMemo(() => chronological.map(hasRetriedFailure), [chronological]);
  const overviewBuckets = useMemo(() => bucketOverviewEvents(
    overviewAxis,
    chronological.map((event) => states.get(event.eventId)?.status ?? 'normal'),
    retriedFlags,
    bucketCount,
  ), [overviewAxis, chronological, states, retriedFlags, bucketCount]);

  const clampStart = useCallback((start: number) => Math.min(maxStart, Math.max(0, start)), [maxStart]);
  const moveWindow = useCallback((start: number) => {
    const next = clampStart(start);
    isAtNewestEnd.current = next >= maxStart;
    setWindowStart(next);
  }, [clampStart, maxStart]);
  const centerOn = useCallback((index: number) => {
    moveWindow(index - visibleCount / 2);
  }, [moveWindow, visibleCount]);

  useLayoutEffect(() => {
    const strip = stripRef.current;
    if (!strip) return undefined;
    const measure = () => setStripWidth(strip.clientWidth || fallbackStripWidth);
    measure();
    if (typeof ResizeObserver === 'undefined') return undefined;
    const observer = new ResizeObserver(measure);
    observer.observe(strip);
    return () => observer.disconnect();
  }, []);

  const centeredHighlightId = useRef<number | null>(null);
  // A new page, or Live adding events, keeps the newest end in view unless the reader moved away from it.
  useEffect(() => {
    const targetIndex = highlightedEventId === null ? undefined : indexByEventId.get(highlightedEventId);
    if (highlightedEventId === null) centeredHighlightId.current = null;
    // A jump centers its event once; later refreshes keep wherever the reader moved the view.
    if (targetIndex !== undefined && centeredHighlightId.current !== highlightedEventId) {
      centeredHighlightId.current = highlightedEventId;
      centerOn(targetIndex);
      return;
    }
    if (isAtNewestEnd.current) setWindowStart(maxStart);
    else setWindowStart((current) => Math.min(maxStart, Math.max(0, current)));
    // Runs when the page changes; selection changes recenter below.
  }, [chronological, maxStart, highlightedEventId]);

  const windowStartRef = useRef(windowStart);
  windowStartRef.current = windowStart;
  const hasEvents = chronological.length > 0;
  // React wheel listeners are passive, so only a native listener can stop the page from scrolling.
  useEffect(() => {
    const strip = stripRef.current;
    if (!strip) return undefined;
    const handleWheel = (event: WheelEvent) => {
      event.preventDefault();
      event.stopPropagation();
      const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
      if (delta !== 0) moveWindow(windowStartRef.current + delta / slotWidth);
    };
    strip.addEventListener('wheel', handleWheel, { passive: false });
    return () => strip.removeEventListener('wheel', handleWheel);
  }, [moveWindow, hasEvents]);

  const selectedIndex = selectedEvent ? indexByEventId.get(selectedEvent.eventId) ?? -1 : -1;
  // Each click or key selection bumps this, so it reveals the event even when the index is unchanged.
  const [revealRequest, setRevealRequest] = useState(0);
  useEffect(() => {
    if (selectedIndex < 0 || isFollowingLatest) return;
    // Selecting an older event stops Live from sliding the view toward newer events.
    isAtNewestEnd.current = selectedIndex === chronological.length - 1;
    if (selectedIndex < windowStart || selectedIndex >= windowStart + visibleCount) centerOn(selectedIndex);
    // Only a selection change recenters, so dragging the overview never snaps back.
  }, [selectedIndex, revealRequest]);

  const computedRelations = useMemo(
    () => relations ?? buildSelectedTimelineRelations(events, selectedEvent, {
      hasOlder: paging?.hasOlder ?? false,
      hasNewer: paging?.hasNewer ?? false,
      prunedEventIdRange,
    }),
    [events, paging?.hasNewer, paging?.hasOlder, prunedEventIdRange, relations, selectedEvent],
  );
  const selectedLinks = computedRelations.links.filter((link) => (
    link.fromEventId === selectedEvent?.eventId || link.toEventId === selectedEvent?.eventId
  ));
  const linkedEventIds = new Set(selectedLinks.flatMap((link) => [link.fromEventId, link.toEventId]));

  const selectIndex = useCallback((index: number) => {
    const event = chronological[Math.min(chronological.length - 1, Math.max(0, index))];
    if (!event) return;
    setRevealRequest((request) => request + 1);
    onSelectEvent(event);
  }, [chronological, onSelectEvent]);

  const handleStripKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const activeIndex = selectedIndex >= 0 ? selectedIndex : Math.min(chronological.length - 1, Math.floor(windowStart) + visibleCount - 1);
    let target: number;
    if (event.key === 'ArrowLeft') target = activeIndex - 1;
    else if (event.key === 'ArrowRight') target = activeIndex + 1;
    else if (event.key === 'PageUp') target = activeIndex - visibleCount;
    else if (event.key === 'PageDown') target = activeIndex + visibleCount;
    else if (event.key === 'Home') target = 0;
    else if (event.key === 'End') target = chronological.length - 1;
    else if (event.key === 'Enter' || event.key === ' ') target = activeIndex;
    else return;
    event.preventDefault();
    selectIndex(target);
  };

  /** The pointer's position along the plot, between the insets, as a ratio in [0, 1]. */
  const pointerRatio = (clientX: number) => {
    const rect = minimapRef.current?.getBoundingClientRect();
    const width = rect && rect.width > 0 ? rect.width : stripWidth;
    const chartX = ((clientX - (rect?.left ?? 0)) / width) * stripWidth;
    return Math.min(1, Math.max(0, (chartX - overviewPlotInset) / plotWidth));
  };
  const brushSpan = () => brushPixelSpan(overviewAxis, windowStart, visibleCount, plotWidth, overviewMinBrushWidth);
  const isOverBrush = (ratio: number) => {
    const { left, right } = brushSpan();
    return ratio * plotWidth >= left && ratio * plotWidth <= right;
  };
  /** The bar under the pointer; on a short page, the nearest event's bar within reach. */
  const bucketAtRatio = (ratio: number): number | null => {
    if (!isFewEvents) {
      const bucketIndex = Math.min(bucketCount - 1, Math.floor(ratio * bucketCount));
      return overviewBuckets[bucketIndex]?.count ? bucketIndex : null;
    }
    // Edges sit halfway between neighbors, so the event whose span holds the pointer is the nearest one.
    const nearest = Math.min(chronological.length - 1, Math.floor(ratioToEventIndex(overviewAxis, ratio)));
    const distance = Math.abs(overviewAxis.positions[nearest] - ratio) * plotWidth;
    if (distance > fewEventBarWidth / 2 + 3) return null;
    return overviewBuckets.findIndex((bucket) => bucket.firstEventIndex <= nearest && bucket.lastEventIndex >= nearest);
  };

  // Pressing inside the brush drags it from where it was grabbed; pressing outside jumps there first.
  const handleMinimapPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    isDraggingMinimap.current = true;
    setHoveredBucket(null);
    event.currentTarget.setPointerCapture?.(event.pointerId);
    const ratio = pointerRatio(event.clientX);
    const pointerIndex = ratioToEventIndex(overviewAxis, ratio);
    if (isOverBrush(ratio)) {
      brushGrabOffset.current = Math.min(visibleCount, Math.max(0, pointerIndex - windowStart));
      return;
    }
    brushGrabOffset.current = visibleCount / 2;
    moveWindow(pointerIndex - brushGrabOffset.current);
  };

  const handleMinimapPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const ratio = pointerRatio(event.clientX);
    if (isDraggingMinimap.current) {
      moveWindow(ratioToEventIndex(overviewAxis, ratio) - brushGrabOffset.current);
      return;
    }
    setHoveredBucket(bucketAtRatio(ratio));
    setIsPointerOverBrush(maxStart > 0 && isOverBrush(ratio));
  };

  const endMinimapDrag = () => {
    isDraggingMinimap.current = false;
  };

  const handleMinimapKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    let next: number;
    if (event.key === 'ArrowLeft') next = windowStart - 1;
    else if (event.key === 'ArrowRight') next = windowStart + 1;
    else if (event.key === 'PageUp') next = windowStart - visibleCount;
    else if (event.key === 'PageDown') next = windowStart + visibleCount;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = maxStart;
    else return;
    event.preventDefault();
    moveWindow(next);
  };

  if (!chronological.length) return <div className="card empty-state"><h3>No semantic events loaded</h3></div>;

  const firstRendered = Math.max(0, Math.floor(windowStart) - overscanSlots);
  const lastRendered = Math.min(chronological.length - 1, Math.ceil(windowStart) + visibleCount + overscanSlots);
  const renderedEvents = chronological.slice(firstRendered, lastRendered + 1);
  const firstVisible = chronological[Math.min(chronological.length - 1, Math.round(windowStart))];
  const lastVisible = chronological[Math.min(chronological.length - 1, Math.round(windowStart) + visibleCount - 1)];
  const xOf = (index: number) => (index - windowStart) * slotWidth + slotWidth / 2;
  const brushInPlot = brushSpan();
  const brush = { left: overviewPlotInset + brushInPlot.left, right: overviewPlotInset + brushInPlot.right };
  // The brush only appears when the page is wider than the strip; otherwise there is nothing to move.
  const hasBrush = maxStart > 0;
  const barSlotWidth = plotWidth / bucketCount;
  const barWidth = isFewEvents ? fewEventBarWidth : Math.max(1, barSlotWidth - (barSlotWidth > 4 ? 2 : 1));
  /** A bar's horizontal center in chart pixels: its own event on a short page, its slice otherwise. */
  const barCenterX = (bucket: OverviewBucket, bucketIndex: number) => overviewPlotInset + (isFewEvents
    ? overviewAxis.positions[bucket.firstEventIndex] * plotWidth
    : (bucketIndex + 0.5) * barSlotWidth);
  const plotX = (index: number) => overviewPlotInset + overviewAxis.positions[index] * plotWidth;
  const maxBucketCount = overviewBuckets.reduce((max, bucket) => Math.max(max, bucket.count), 0);
  const problemCount = overviewBuckets.reduce((sum, bucket) => sum + bucket.failed + bucket.retried, 0);
  const [axisStartLabel, axisEndLabel] = overviewAxisEnds(overviewAxis, chronological, timezone);
  const axisSpanLabel = [
    overviewAxis.startMs !== null && overviewAxis.endMs !== null && overviewAxis.endMs > overviewAxis.startMs
      ? formatElapsedDuration(overviewAxis.endMs - overviewAxis.startMs)
      : '',
    overviewAxis.kind === 'index' ? 'spaced by event order' : '',
  ].filter(Boolean).join(' · ');
  const hovered = hoveredBucket !== null && overviewBuckets[hoveredBucket]?.count
    ? overviewBucketLines(overviewAxis, overviewBuckets[hoveredBucket], hoveredBucket, bucketCount, chronological, timezone)
    : null;
  const hoveredCenterRatio = hoveredBucket === null || !overviewBuckets[hoveredBucket]?.count
    ? 0
    : barCenterX(overviewBuckets[hoveredBucket], hoveredBucket) / stripWidth;

  return (
    <div className="timeline-wrap htl">
      {prunedEventIdRange !== undefined && chronological[0]?.eventId === 1 && (
        <div className="warning-banner timeline-pruned-banner" role="note">
          {prunedEventIdRange.firstEventId === prunedEventIdRange.lastEventId
            ? `Event #${prunedEventIdRange.firstEventId} was`
            : `Events #${prunedEventIdRange.firstEventId} to #${prunedEventIdRange.lastEventId} were`} removed by the history window; FlowStart is kept.
        </div>
      )}

      <EventKindLegend />
      <div
        aria-label="Timeline overview"
        aria-valuemax={maxStart}
        aria-valuemin={0}
        aria-valuenow={Math.round(windowStart)}
        aria-valuetext={`Events #${firstVisible.eventId} to #${lastVisible.eventId}`}
        className={`htl-minimap${isPointerOverBrush ? ' is-over-brush' : ''}`}
        data-axis={overviewAxis.kind}
        onKeyDown={handleMinimapKeyDown}
        onLostPointerCapture={endMinimapDrag}
        onPointerDown={handleMinimapPointerDown}
        onPointerLeave={() => {
          setHoveredBucket(null);
          setIsPointerOverBrush(false);
        }}
        onPointerMove={handleMinimapPointerMove}
        onPointerUp={endMinimapDrag}
        ref={minimapRef}
        role="slider"
        tabIndex={0}
      >
        <svg aria-hidden="true" className="htl-overview-chart" height={overviewHeight} viewBox={`0 0 ${stripWidth} ${overviewHeight}`}>
          <line
            className="htl-overview-baseline"
            x1={overviewPlotInset}
            x2={overviewPlotInset + plotWidth}
            y1={overviewHeight - 0.5}
            y2={overviewHeight - 0.5}
          />
          {overviewBuckets.flatMap((bucket, bucketIndex) => {
            if (!bucket.count) return [];
            const segments = overviewBarSegments(bucket, maxBucketCount);
            const centerX = barCenterX(bucket, bucketIndex);
            const x = centerX - barWidth / 2;
            const isInView = bucket.lastEventIndex >= Math.floor(windowStart) && bucket.firstEventIndex < windowStart + visibleCount;
            const hasFailure = bucket.failed + bucket.retried > 0;
            const bandWidth = Math.max(barSlotWidth, barWidth + 4);
            let y = overviewHeight - segments.reduce((sum, segment) => sum + segment.height, 0);
            return [(
              <g
                className={[
                  'htl-bar',
                  isInView && hasBrush ? 'is-in-view' : '',
                  hoveredBucket === bucketIndex ? 'is-hovered' : '',
                  hasFailure ? 'has-failure' : '',
                ].filter(Boolean).join(' ')}
                data-count={bucket.count}
                key={bucketIndex}
              >
                {hasFailure && (
                  <rect
                    className="htl-bar-failure-band"
                    height={overviewHeight - overviewMarkerLaneHeight}
                    rx={2}
                    width={isFewEvents ? barWidth + 4 : bandWidth}
                    x={centerX - (isFewEvents ? barWidth + 4 : bandWidth) / 2}
                    y={overviewMarkerLaneHeight}
                  />
                )}
                {(hasFailure || bucket.waiting > 0) && (
                  <circle
                    className={`htl-bar-marker htl-status-${hasFailure ? 'failed' : 'waiting'}`}
                    cx={centerX}
                    cy={overviewMarkerLaneHeight / 2}
                    r={3}
                  />
                )}
                {segments.map((segment) => {
                  const top = y;
                  y += segment.height;
                  return (
                    <rect
                      className={`htl-bar-segment htl-status-${segment.tone}`}
                      height={segment.height}
                      key={segment.tone}
                      rx={Math.min(isFewEvents ? 3 : 1.5, barWidth / 3)}
                      width={barWidth}
                      x={x}
                      y={top}
                    />
                  );
                })}
              </g>
            )];
          })}
          {[...linkedEventIds].flatMap((eventId) => {
            const index = indexByEventId.get(eventId);
            if (index === undefined || index === selectedIndex) return [];
            const x = plotX(index);
            return [<line className="htl-overview-linked" key={eventId} x1={x} x2={x} y1={overviewMarkerLaneHeight} y2={overviewHeight} />];
          })}
          {selectedIndex >= 0 && (
            <g className="htl-overview-selection">
              <line x1={plotX(selectedIndex)} x2={plotX(selectedIndex)} y1={overviewMarkerLaneHeight - 2} y2={overviewHeight} />
              <circle cx={plotX(selectedIndex)} cy={overviewMarkerLaneHeight - 2} r={2.5} />
            </g>
          )}
          {hasBrush && (
            <g className="htl-overview-brush">
              <rect className="htl-overview-brush-fill" height={overviewHeight} rx={3} width={brush.right - brush.left} x={brush.left} y={0} />
              {[brush.left, brush.right].map((edge, edgeIndex) => (
                <g className="htl-overview-brush-edge" key={edgeIndex}>
                  <line x1={edge} x2={edge} y1={0} y2={overviewHeight} />
                  {/* Grips sit just outside the brush, in the inset at either end, so they never cover a bar in view. */}
                  <rect height={16} rx={2} width={5} x={edgeIndex === 0 ? edge - 6 : edge + 1} y={(overviewHeight - 16) / 2} />
                </g>
              ))}
            </g>
          )}
        </svg>
        <div aria-hidden="true" className="htl-overview-axis">
          <span>{axisStartLabel}</span>
          <span className="htl-overview-summary">
            {axisSpanLabel}
            {problemCount > 0 && (
              <>
                {axisSpanLabel ? ' · ' : ''}
                <span className="htl-overview-summary-failed">
                  {problemCount} {problemCount === 1 ? 'failure' : 'failures'}
                </span>
              </>
            )}
          </span>
          <span>{axisEndLabel}</span>
        </div>
        {hovered && (
          <div
            aria-hidden="true"
            className={`htl-overview-tooltip${hoveredCenterRatio < 0.12 ? ' is-start' : ''}${hoveredCenterRatio > 0.88 ? ' is-end' : ''}`}
            style={{ left: `${hoveredCenterRatio * 100}%` }}
          >
            <strong>{hovered.range}</strong>
            <span>{hovered.detail}</span>
            {hovered.problems.map((problem) => (
              <span className={`htl-overview-tooltip-problem htl-status-${problem.tone}`} key={problem.text}>{problem.text}</span>
            ))}
          </div>
        )}
      </div>

      <div
        aria-activedescendant={selectedEvent && selectedIndex >= firstRendered && selectedIndex <= lastRendered
          ? `htl-event-${selectedEvent.eventId}`
          : undefined}
        aria-describedby={helpId}
        aria-label="Timeline events, oldest on the left"
        className="htl-strip"
        style={{
          '--htl-label-angle': `-${labelAngleDegrees}deg`,
          '--htl-label-max-width': `${labelMaxWidth}px`,
          '--htl-slot-width': `${slotWidth}px`,
          height: `${horizontalTimelineLayout.stripHeight}px`,
        } as CSSProperties}
        onKeyDown={handleStripKeyDown}
        ref={stripRef}
        role="listbox"
        tabIndex={0}
      >
        {renderedEvents.map((event, offset) => {
          const index = firstRendered + offset;
          const kind = eventKindOf(event);
          const state = states.get(event.eventId) ?? normalState;
          const status = state.status;
          const KindIcon = eventKinds[kind].icon;
          const subject = eventSubject(event);
          const isSelected = index === selectedIndex;
          const isRetried = status === 'normal' && retriedFlags[index];
          const classes = [
            'htl-event',
            `kind-${kind}`,
            isSelected ? 'selected' : '',
            linkedEventIds.has(event.eventId) && !isSelected ? 'linked' : '',
            highlightedEventId === event.eventId ? 'jump-highlighted' : '',
          ].filter(Boolean).join(' ');
          return (
            <div
              aria-label={[
                eventTitle(event),
                subject !== eventTitle(event) ? subject : '',
                state.spoken,
                isRetried ? retriedSpoken(event) : '',
                `#${event.eventId}`,
              ].filter(Boolean).join(', ')}
              aria-selected={isSelected}
              className={classes}
              data-event-id={event.eventId}
              id={`htl-event-${event.eventId}`}
              key={event.eventId}
              onClick={() => selectIndex(index)}
              role="option"
              style={{ left: `${(index - windowStart) * slotWidth}px` }}
            >
              <span aria-hidden="true" className="htl-label">{subject}</span>
              <span aria-hidden="true" className={`htl-dot htl-family-${eventKinds[kind].family} htl-status-${status}`}>
                <KindIcon size={13} strokeWidth={2.5} />
                {status === 'failed' && <span className="htl-dot-badge"><X size={8} strokeWidth={3.5} /></span>}
                {isRetried && <span className="htl-dot-badge htl-dot-badge-retried"><RotateCcw size={7} strokeWidth={3.5} /></span>}
              </span>
            </div>
          );
        })}
        <div aria-hidden="true" className="htl-axis">
          {renderedEvents.flatMap((event, offset) => {
            const index = firstRendered + offset;
            const isSelected = index === selectedIndex;
            const isNextToSelection = selectedIndex >= 0 && Math.abs(index - selectedIndex) === 1;
            if (!isSelected && (event.eventId % axisEventInterval !== 0 || isNextToSelection)) return [];
            return [(
              <span
                className={`htl-axis-label${isSelected ? ' selected' : ''}`}
                key={event.eventId}
                style={{ left: `${(index - windowStart) * slotWidth + slotWidth / 2}px` }}
              >
                #{event.eventId}
              </span>
            )];
          })}
        </div>
        <svg aria-hidden="true" className="htl-arcs" height={arcBandHeight} width={stripWidth}>
          {selectedLinks.flatMap((link) => {
            const fromIndex = indexByEventId.get(link.fromEventId);
            const toIndex = indexByEventId.get(link.toEventId);
            if (fromIndex === undefined || toIndex === undefined) return [];
            const fromX = xOf(fromIndex);
            const toX = xOf(toIndex);
            const depth = Math.min(arcBandHeight - 6, 12 + Math.abs(toIndex - fromIndex) * 2);
            return [(
              <path
                className={`htl-arc kind-${link.kind}`}
                d={`M ${fromX} 2 C ${fromX} ${depth}, ${toX} ${depth}, ${toX} 2`}
                key={`${link.kind}-${link.fromEventId}-${link.toEventId}`}
              />
            )];
          })}
        </svg>
      </div>

      <p className="sr-only" id={helpId}>
        Left and Right move the selection; Page Up and Page Down move by a screen.
      </p>
    </div>
  );
}
