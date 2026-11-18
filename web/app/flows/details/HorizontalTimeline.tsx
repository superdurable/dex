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
import { formatDate, isSameCalendarDay, type TimezonePreference } from '@/lib/format';
import type { EventIdRange } from '@/lib/historyRelations';
import {
  buildSelectedTimelineRelations,
  formatElapsedDuration,
  type TimelineSelectedRelations,
} from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import { ChevronRight, RotateCcw, X } from 'lucide-react';
import { flowStatusLabel } from '@/lib/semantic';
import { eventTimeTravelTarget } from '@/lib/timeTravel';
import { useTimezonePreference } from '../../providers';
import { eventTitle } from './EventDetails';
import { executionSummary, type TimelinePaging } from './Timeline';
import {
  brushPixelSpan,
  bucketOverviewEvents,
  buildOverviewAxis,
  laneOverviewAxis,
  perEventOverviewBuckets,
  timelineSlotWidth,
  eventIndexToRatio,
  hasRetriedFailure,
  laneIndexToPageIndex,
  minimumPositionGap,
  overviewBucketSelectionIndex,
  pageIndexToLaneIndex,
  ratioToEventIndex,
  type EventStatus,
  type OverviewAxis,
  type OverviewBucket,
  type TimelineLabelMode,
} from './timelineOverview';
import { persistTimelineLabelMode, storedTimelineLabelMode } from './timelineLabelModeStorage';
import { TimelineLabelModeIntro, TimelineLabelModeTabs, useTimelineLabelModeIntro } from './TimelineLabelModeTabs';
import { eventKindOf, eventKinds, namesIn, timelineEventKindOrder } from './timelineEventKinds';
import {
  allTimelineEventsFilter,
  compileTimelineEventFilter,
  filteredKindCount,
  isCompiledTimelineEventShown,
  persistTimelineEventFilter,
  storedTimelineEventFilter,
  timelineEventFilterOptions,
  timelinePageKindCounts,
  type TimelineEventFilter,
} from './timelineEventFilter';
import { TimelineEventFilterMenu } from './TimelineEventFilterMenu';

const arcBandHeight = 28;
const axisBandHeight = 18;
const dotBandHeight = 30;
/** Dense: each event's label slants up at this angle, like angled parking, so long names fit side by side. */
const denseLabelAngleDegrees = 30;
const denseLabelMaxWidth = 280;
const denseLabelAngle = (denseLabelAngleDegrees * Math.PI) / 180;
/** About the width of one Dense label character, for sizing the band before layout. */
const denseLabelCharacterWidth = 7;

/**
 * Returns the Dense strip height for the longest label on the page: its estimated width, at most
 * denseLabelMaxWidth, rises at the label angle. The whole page sets it, so scrolling never changes it.
 */
export function denseStripHeight(subjects: readonly string[]): number {
  const longest = subjects.reduce((max, subject) => Math.max(max, labelWidthUnits(subject)), 0);
  const labelWidth = Math.min(denseLabelMaxWidth, longest * denseLabelCharacterWidth + 8);
  return Math.ceil(labelWidth * Math.sin(denseLabelAngle)) + 12 + dotBandHeight + axisBandHeight + arcBandHeight;
}

/** Tidy: each label wraps inside its event's column, centered over the dot, up to this many lines. */
const labelMaxLines = 3;
const labelLineHeight = 15;
/** The label column is this much narrower than the slot, so neighbors never touch. */
const labelColumnGap = 6;
/** About the width of one Tidy label character at the label font, for sizing the band before layout. */
const labelCharacterWidth = 6.6;

/** Returns how many lines a label takes in a column, estimated word by word, at most labelMaxLines. */
export function labelLineCount(label: string, columnWidth: number): number {
  const lineUnits = Math.max(1, Math.floor(columnWidth / labelCharacterWidth));
  let lines = 1;
  let used = 0;
  for (const word of label.split(' ').filter(Boolean)) {
    let units = labelWidthUnits(word);
    if (used > 0 && used + 1 + units <= lineUnits) {
      used += 1 + units;
      continue;
    }
    if (used > 0) lines += 1;
    // A word wider than the column breaks inside itself across lines.
    while (units > lineUnits) {
      lines += 1;
      units -= lineUnits;
    }
    used = units;
  }
  return Math.min(labelMaxLines, lines);
}

/**
 * Returns the Tidy strip height for the page's tallest wrapped label, from one to labelMaxLines lines.
 * The whole page sets it, so scrolling never changes it.
 */
export function tidyStripHeight(labels: readonly string[], slotWidth: number): number {
  const columnWidth = slotWidth - labelColumnGap;
  const lines = labels.reduce((max, label) => Math.max(max, labelLineCount(label, columnWidth)), 1);
  return lines * labelLineHeight + 12 + dotBandHeight + axisBandHeight + arcBandHeight;
}

/**
 * Returns the Tidy slot width that keeps the page's longest word on one line, without a soft hyphen.
 * Words with wide characters wrap anywhere, so they never widen the slot.
 */
export function tidyWordSlotWidth(labels: readonly string[]): number {
  let longestUnits = 0;
  for (const label of labels) {
    for (const word of label.split(/\s+/)) {
      if (word && !hasWideCharacter(word)) longestUnits = Math.max(longestUnits, labelWidthUnits(word));
    }
  }
  return Math.ceil(longestUnits * labelCharacterWidth) + labelColumnGap;
}

/** True when the word holds an East Asian wide character, which wraps between any two characters. */
function hasWideCharacter(word: string): boolean {
  return labelWidthUnits(word) !== [...word].length;
}

/**
 * Returns the label with soft hyphens in each word wider than its column, splitting it into even parts,
 * so the word breaks with a hyphen and never leaves one or two letters alone on a line.
 */
export function labelWithBreakHints(label: string, columnWidth: number): string {
  const lineUnits = Math.max(1, Math.floor(columnWidth / labelCharacterWidth));
  return label.split(' ').map((word) => {
    const characters = [...word];
    if (characters.length <= lineUnits || hasWideCharacter(word)) return word;
    const partCount = Math.ceil(characters.length / lineUnits);
    const partLength = Math.ceil(characters.length / partCount);
    const parts: string[] = [];
    for (let start = 0; start < characters.length; start += partLength) parts.push(characters.slice(start, start + partLength).join(''));
    return parts.join('\u00AD');
  }).join(' ');
}

/** Splits an identifier into words: snake_case, kebab-case, camelCase and PascalCase, keeping acronyms. */
function identifierWords(identifier: string): string[] {
  return identifier
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .split(/[\s_-]+/)
    .filter(Boolean);
}

/**
 * Returns an event subject as words for the lane, such as "Send User Message" for send_user_message
 * or SendUserMessage. Text that already has spaces stays as it is. The exact name stays in the tooltip.
 */
export function timelineLabelText(subject: string): string {
  return subject.split(', ').map((part) => {
    if (/\s/.test(part) || !/[_-]|[a-z][A-Z]|[A-Z]{2}[a-z]/.test(part)) return part;
    return identifierWords(part).map((word) => word[0].toUpperCase() + word.slice(1)).join(' ');
  }).join(', ');
}

/** East Asian wide and fullwidth ranges, which take about two label characters each. */
const wideCodePointRanges: readonly (readonly [number, number])[] = [
  [0x1100, 0x115f],
  [0x2e80, 0x303e],
  [0x3041, 0x33ff],
  [0x3400, 0x4dbf],
  [0x4e00, 0x9fff],
  [0xa000, 0xa4cf],
  [0xac00, 0xd7a3],
  [0xf900, 0xfaff],
  [0xfe30, 0xfe4f],
  [0xff00, 0xff60],
  [0xffe0, 0xffe6],
  [0x20000, 0x3fffd],
];

/** Returns the label's width in label characters, counting each wide or fullwidth character as 2. */
export function labelWidthUnits(subject: string): number {
  let units = 0;
  for (const character of subject) {
    const codePoint = character.codePointAt(0) ?? 0;
    units += wideCodePointRanges.some(([low, high]) => codePoint >= low && codePoint <= high) ? 2 : 1;
  }
  return units;
}

/** The strip geometry: labels on top, then dots, event numbers, and link arcs. */
export const horizontalTimelineLayout = {
  denseLabelAngleDegrees,
  denseLabelMaxWidth,
  labelMaxLines,
  /** Room at the right edge: Dense labels of the newest events slant past their dots. */
  labelRunWidth: {
    dense: Math.ceil(denseLabelMaxWidth * Math.cos(denseLabelAngle)) + 10,
    tidy: 0,
  } satisfies Record<TimelineLabelMode, number>,
};

/** Returns the room at the right edge for the run's end note: its estimated width, at most 240 px. */
export function endNoteRunWidth(endNoteLabel: string): number {
  // The note starts 18 px past the newest dot; its mark and padding take about 28 px.
  return Math.min(240, 18 + 28 + Math.ceil(labelWidthUnits(endNoteLabel) * 7));
}
const overscanSlots = 6;

const fallbackStripWidth = 1200;
/** The overview: a marker lane for problems above the bars, about one bar per 7 px. */
const overviewMarkerLaneHeight = 4;
const overviewBarAreaHeight = 14;
const overviewHeight = overviewMarkerLaneHeight + overviewBarAreaHeight;
const overviewBucketWidth = 7;
const overviewMinBarHeight = 2;
/** A failed, waiting, or canceled share never shrinks below this, however many normal events share its bar. */
const overviewStatusCapHeight = 3;
const overviewMinBrushWidth = 24;
/** The brush grips reach this far outside either brush edge, so pressing one grabs the brush. */
const overviewBrushGripReach = 6;
/** A press that moves farther than this is a drag, not a click on a bar. */
const overviewClickSlop = 4;
/** Room at both ends, so the first and last bars clear the brush handles. */
const overviewPlotInset = 10;
/** On a short page each event gets its own bar; time spacing must leave neighbors this many pixels apart. */
const overviewFewEventMinGap = 10;
const overviewFewEventMaxBarWidth = 20;

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

/** The axis names every tenth event, so the subject labels need no numbers. */
const axisEventInterval = 10;

/** Consecutive events about the same subject, which share one flat label. */
export interface SubjectRun {
  firstIndex: number;
  lastIndex: number;
  subject: string;
}

/** What the run is doing at the newest end of the lane, and the event that says so. */
export interface TimelineEndNote {
  /** A few words beside the newest event, such as "waiting · fires in 2d 6h". */
  label: string;
  eventId: number;
  isOpen: boolean;
}

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

/** The icon and colour key, a button that opens over the lane so the timeline leads. */
function EventKindLegend() {
  return (
    <details className="htl-legend-fold htl-popover">
      <summary>Legend</summary>
    <div className="htl-legend">
      <ul aria-label="Event kinds">
        {timelineEventKindOrder.map((kind) => {
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
    </details>
  );
}

/** How a retried Step event states its failed attempts after the subject; only a normal event succeeded. */
function retriedSpoken(event: FlowHistoryEvent, status: EventStatus): string {
  if (status !== 'normal') return 'after a failed attempt';
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
  if (!isSameCalendarDay(axis.startMs, axis.endMs, timezone)) {
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
  timezone: TimezonePreference,
): string | null {
  if (axis.startMs === null || axis.endMs === null || axis.eventTimes === null) return null;
  const span = axis.endMs - axis.startMs;
  let from = axis.startMs + (span * bucketIndex) / bucketCount;
  let to = axis.startMs + (span * (bucketIndex + 1)) / bucketCount;
  if (axis.kind === 'index' || bucket.count === 1) {
    from = axis.eventTimes[bucket.firstEventIndex];
    to = axis.eventTimes[bucket.lastEventIndex];
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
  hiddenCount = 0,
): { range: string; detail: string; hidden: string; problems: { tone: Exclude<EventStatus, 'normal'>; text: string }[] } {
  const firstId = events[bucket.firstEventIndex].eventId;
  const lastId = events[bucket.lastEventIndex].eventId;
  const ids = firstId === lastId ? `#${firstId}` : `#${firstId} – #${lastId}`;
  const eventCount = `${bucket.count} ${bucket.count === 1 ? 'event' : 'events'}`;
  const timeRange = overviewBucketTimeRange(axis, bucket, bucketIndex, bucketCount, timezone);
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
  let hidden = '';
  if (hiddenCount > 0) hidden = bucket.count === 1 ? 'Hidden by the filter' : `${hiddenCount} hidden by the filter`;
  return { range, detail, hidden, problems };
}

/** One bar's stacked segments from the top: failures, waits, cancels, then normal events. */
function overviewBarSegments(bucket: OverviewBucket, maxCount: number): { tone: EventStatus; height: number }[] {
  const scaled = Math.round((bucket.count / Math.max(1, maxCount)) * (overviewBarAreaHeight - 2));
  let height = Math.max(overviewMinBarHeight, scaled);
  const shares: [Exclude<EventStatus, 'normal'>, number][] = [
    ['failed', bucket.failed + bucket.retriedNormal],
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

/** Shows a Timeline page left to right with Dense or Tidy labels; only events in view render. */
export function HorizontalTimeline({
  events,
  selectedEvent,
  onSelectEvent,
  relations,
  paging,
  prunedEventIdRange,
  highlightedEventId = null,
  isFollowingLatest = false,
  endNote,
  onShowEndNoteEvent,
  onTimeTravelFromEvent,
  flowType,
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
  /** What the run is doing now, shown at the lane's newest end on the newest page. */
  endNote?: TimelineEndNote | null;
  onShowEndNoteEvent?: (eventId: number) => void;
  /** Opens time travel at an event; T calls it for the selected event. */
  onTimeTravelFromEvent?: (event: FlowHistoryEvent) => void;
  /** The run's Flow type; the event filter is saved per Flow type. */
  flowType?: string;
}) {
  const [labelMode, setLabelMode] = useState<TimelineLabelMode>(storedTimelineLabelMode);
  const { isIntroShown: isLabelModeIntroShown, dismissIntro: dismissLabelModeIntro } = useTimelineLabelModeIntro();
  const labelModeIntroId = useId();
  const changeLabelMode = useCallback((nextLabelMode: TimelineLabelMode) => {
    // Choosing a mode shows the reader has found the tabs, so the intro is done.
    if (isLabelModeIntroShown) dismissLabelModeIntro();
    setLabelMode(nextLabelMode);
    persistTimelineLabelMode(nextLabelMode);
  }, [dismissLabelModeIntro, isLabelModeIntroShown]);
  const [eventFilter, setEventFilter] = useState<TimelineEventFilter>(() => storedTimelineEventFilter(flowType));
  const [eventFilterFlowType, setEventFilterFlowType] = useState(flowType);
  if (eventFilterFlowType !== flowType) {
    // Another Flow type has its own saved filter.
    setEventFilterFlowType(flowType);
    setEventFilter(storedTimelineEventFilter(flowType));
  }
  const [isFilterChanging, setIsFilterChanging] = useState(false);
  const filterChangeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (filterChangeTimer.current) clearTimeout(filterChangeTimer.current);
  }, []);
  const changeEventFilter = useCallback((nextFilter: TimelineEventFilter) => {
    setEventFilter(nextFilter);
    persistTimelineEventFilter(flowType, nextFilter);
    // Events glide to their new places only during a filter change, so scrolling never lags.
    setIsFilterChanging(true);
    if (filterChangeTimer.current) clearTimeout(filterChangeTimer.current);
    filterChangeTimer.current = setTimeout(() => setIsFilterChanging(false), 320);
  }, [flowType]);
  const isTidy = labelMode === 'tidy';
  // Only an open run has a "now" to note; a closed run's last event already says how it ended.
  const shownEndNote = endNote?.isOpen && !paging?.hasNewer ? endNote : null;
  const labelRunWidth = shownEndNote
    ? Math.max(horizontalTimelineLayout.labelRunWidth[labelMode], endNoteRunWidth(shownEndNote.label))
    : horizontalTimelineLayout.labelRunWidth[labelMode];
  const pageEvents = useMemo(() => [...events].sort((left, right) => left.eventId - right.eventId), [events]);
  const states = useMemo(() => eventStates(pageEvents, paging?.hasNewer ?? false), [pageEvents, paging?.hasNewer]);
  const compiledEventFilter = useMemo(() => compileTimelineEventFilter(eventFilter), [eventFilter]);
  const pageShownFlags = useMemo(
    () => pageEvents.map((event) => isCompiledTimelineEventShown(compiledEventFilter, event)),
    [compiledEventFilter, pageEvents],
  );
  const pageKindCounts = useMemo(() => timelinePageKindCounts(pageEvents), [pageEvents]);
  const filterOptions = useMemo(
    () => timelineEventFilterOptions(compiledEventFilter, pageKindCounts),
    [compiledEventFilter, pageKindCounts],
  );
  const selectedEventId = selectedEvent?.eventId ?? null;
  /** The lane: events the filter shows, plus the selected and the jumped-to event, which always show. */
  const { laneEvents, laneToPage } = useMemo(() => {
    const lane: FlowHistoryEvent[] = [];
    const pageIndexes: number[] = [];
    pageEvents.forEach((event, pageIndex) => {
      if (!pageShownFlags[pageIndex] && event.eventId !== selectedEventId && event.eventId !== highlightedEventId) return;
      lane.push(event);
      pageIndexes.push(pageIndex);
    });
    return { laneEvents: lane, laneToPage: pageIndexes };
  }, [highlightedEventId, pageEvents, pageShownFlags, selectedEventId]);
  /** Lane event IDs at the last commit, so a filter change fades in only added events. */
  const previousLaneEventIds = useRef<ReadonlySet<number>>(new Set());
  useEffect(() => {
    previousLaneEventIds.current = new Set(laneEvents.map((event) => event.eventId));
  }, [laneEvents]);
  const pageLaneFlags = useMemo(() => {
    const flags = pageEvents.map(() => false);
    for (const pageIndex of laneToPage) flags[pageIndex] = true;
    return flags;
  }, [laneToPage, pageEvents]);
  const hiddenEventCount = pageEvents.length - laneEvents.length;
  const isLaneFiltered = hiddenEventCount > 0;
  const indexByEventId = useMemo(
    () => new Map(laneEvents.map((event, index) => [event.eventId, index])),
    [laneEvents],
  );
  const pageIndexByEventId = useMemo(
    () => new Map(pageEvents.map((event, index) => [event.eventId, index])),
    [pageEvents],
  );
  const stripRef = useRef<HTMLDivElement>(null);
  const helpId = useId();
  const overviewHelpId = useId();
  const minimapRef = useRef<HTMLDivElement>(null);
  const [stripWidth, setStripWidth] = useState(fallbackStripWidth);
  const pageSubjects = useMemo(() => pageEvents.map(eventSubject), [pageEvents]);
  const pageLabels = useMemo(() => (isTidy ? pageSubjects.map(timelineLabelText) : pageSubjects), [isTidy, pageSubjects]);
  const labels = useMemo(() => laneToPage.map((pageIndex) => pageLabels[pageIndex]), [laneToPage, pageLabels]);
  // The whole page sets it, so scrolling never changes the slot width.
  // Hidden events count too, so filtering never changes it either.
  const pageMinimumSlotWidth = useMemo(() => (isTidy ? tidyWordSlotWidth(pageLabels) : 0), [isTidy, pageLabels]);
  const slotWidth = timelineSlotWidth(laneEvents.length, stripWidth - labelRunWidth, labelMode, pageMinimumSlotWidth);
  const visibleCount = Math.max(1, Math.floor((stripWidth - labelRunWidth) / slotWidth));
  const maxStart = Math.max(0, laneEvents.length - visibleCount);
  // A page that fits draws each overview bar under its own dot; a longer page's overview is a minimap.
  // Hidden events have no dot, so a filtered lane always gets the page-wide minimap.
  const isLaneOverview = maxStart === 0 && !isLaneFiltered;
  const [windowStart, setWindowStart] = useState(maxStart);
  const isAtNewestEnd = useRef(true);
  const isDraggingMinimap = useRef(false);
  /** The press: view start, plus its left edge and the pointer in plot pixels. A drag shifts the edge by the pointer delta. */
  const brushDragOrigin = useRef({ windowStart: 0, pointerX: 0, windowLeftX: 0, hasDragged: false, isGripPress: false });
  const [hoveredBucket, setHoveredBucket] = useState<number | null>(null);
  const [isPointerOverBrush, setIsPointerOverBrush] = useState(false);
  const timezone = useTimezonePreference();
  const plotWidth = Math.max(1, stripWidth - 2 * overviewPlotInset);
  const histogramBucketCount = Math.max(1, Math.floor(plotWidth / overviewBucketWidth));
  // A page with fewer events than bars draws one wide bar per event instead of slivers.
  const isFewEvents = isLaneOverview || pageEvents.length <= histogramBucketCount;
  const overviewAxis = useMemo(
    () => (isLaneOverview
      ? laneOverviewAxis(pageEvents, slotWidth, overviewPlotInset, plotWidth)
      : buildOverviewAxis(pageEvents, histogramBucketCount, isFewEvents ? overviewFewEventMinGap / plotWidth : 0)),
    [pageEvents, histogramBucketCount, isFewEvents, isLaneOverview, plotWidth, slotWidth],
  );
  const bucketCount = isLaneOverview || (isFewEvents && overviewAxis.kind === 'index')
    ? Math.max(1, pageEvents.length)
    : histogramBucketCount;
  /** Each event's own bar width on a short page: as wide as its neighbors allow, up to the cap. */
  const fewEventBarWidth = isFewEvents
    ? Math.max(3, Math.min(overviewFewEventMaxBarWidth, minimumPositionGap(overviewAxis) * plotWidth - 3))
    : 0;
  const retriedFlags = useMemo(() => pageEvents.map(hasRetriedFailure), [pageEvents]);
  const stripHeight = useMemo(
    () => (isTidy ? tidyStripHeight(labels, slotWidth) : denseStripHeight(labels)),
    [isTidy, labels, slotWidth],
  );
  const statusList = useMemo(
    () => pageEvents.map((event) => states.get(event.eventId)?.status ?? 'normal'),
    [pageEvents, states],
  );
  const overviewBuckets = useMemo(
    () => (isLaneOverview
      ? perEventOverviewBuckets(statusList, retriedFlags)
      : bucketOverviewEvents(overviewAxis, statusList, retriedFlags, bucketCount)),
    [bucketCount, isLaneOverview, overviewAxis, retriedFlags, statusList],
  );
  /** How many events of each bar the lane leaves out, so the overview can dim them. */
  const bucketHiddenCounts = useMemo(() => overviewBuckets.map((bucket) => {
    if (!isLaneFiltered || bucket.count === 0) return 0;
    let hidden = 0;
    for (let index = bucket.firstEventIndex; index <= bucket.lastEventIndex; index += 1) if (!pageLaneFlags[index]) hidden += 1;
    return hidden;
  }), [isLaneFiltered, overviewBuckets, pageLaneFlags]);
  const lanePageIndex = (laneIndex: number) => laneIndexToPageIndex(laneToPage, pageEvents.length, laneIndex);

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
  }, [laneEvents, maxStart, highlightedEventId]);

  const windowStartRef = useRef(windowStart);
  windowStartRef.current = windowStart;
  const hasEvents = pageEvents.length > 0;
  // React wheel listeners are passive, so only a native listener can stop the page from scrolling.
  useEffect(() => {
    const strip = stripRef.current;
    if (!strip) return undefined;
    const handleWheel = (event: WheelEvent) => {
      // A vertical wheel scrolls the page past the lane; sideways swipes and Shift+wheel move the lane.
      const isSideways = Math.abs(event.deltaX) > Math.abs(event.deltaY);
      if (!isSideways && !event.shiftKey) return;
      event.preventDefault();
      event.stopPropagation();
      const delta = isSideways ? event.deltaX : event.deltaY;
      if (delta !== 0) moveWindow(windowStartRef.current + delta / slotWidth);
    };
    strip.addEventListener('wheel', handleWheel, { passive: false });
    return () => strip.removeEventListener('wheel', handleWheel);
  }, [moveWindow, hasEvents, slotWidth]);

  const selectedIndex = selectedEvent ? indexByEventId.get(selectedEvent.eventId) ?? -1 : -1;
  const selectedPageIndex = selectedEvent ? pageIndexByEventId.get(selectedEvent.eventId) ?? -1 : -1;
  // Each click or key selection bumps this, so it reveals the event even when the index is unchanged.
  const [revealRequest, setRevealRequest] = useState(0);
  useEffect(() => {
    if (selectedIndex < 0 || isFollowingLatest) return;
    if (selectedIndex < windowStart || selectedIndex >= windowStart + visibleCount) centerOn(selectedIndex);
    // Selecting an older event stops Live from sliding the view toward newer events.
    // The page's newest event decides, since the lane's last event may be followed by hidden ones.
    isAtNewestEnd.current = selectedPageIndex === pageEvents.length - 1;
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

  const selectEvent = useCallback((event: FlowHistoryEvent) => {
    setRevealRequest((request) => request + 1);
    onSelectEvent(event);
  }, [onSelectEvent]);
  const selectIndex = useCallback((index: number) => {
    const event = laneEvents[Math.min(laneEvents.length - 1, Math.max(0, index))];
    if (event) selectEvent(event);
  }, [laneEvents, selectEvent]);

  /** J selects the older neighbour and K the newer one; T time travels from the selection. */
  const handlePageKey = (key: string): boolean => {
    if (key === 't') {
      if (!selectedEvent || !onTimeTravelFromEvent || !eventTimeTravelTarget(selectedEvent)) return false;
      onTimeTravelFromEvent(selectedEvent);
      return true;
    }
    const activeIndex = selectedIndex >= 0 ? selectedIndex : laneEvents.length - 1;
    const target = key === 'j' ? activeIndex - 1 : activeIndex + 1;
    if (target < 0 || target >= laneEvents.length) return false;
    selectIndex(target);
    return true;
  };
  const handlePageKeyRef = useRef(handlePageKey);
  handlePageKeyRef.current = handlePageKey;

  // J, K and T work from anywhere on the page, except while typing or in a dialog.
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey || !['j', 'k', 't'].includes(event.key)) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest('input, textarea, select, [contenteditable="true"], [role="dialog"]')) return;
      if (handlePageKeyRef.current(event.key)) event.preventDefault();
    };
    document.addEventListener('keydown', listener);
    return () => document.removeEventListener('keydown', listener);
  }, []);

  const handleStripKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const activeIndex = selectedIndex >= 0 ? selectedIndex : Math.min(laneEvents.length - 1, Math.floor(windowStart) + visibleCount - 1);
    let target: number;
    if (event.key === 'ArrowLeft') target = activeIndex - 1;
    else if (event.key === 'ArrowRight') target = activeIndex + 1;
    else if (event.key === 'PageUp') target = activeIndex - visibleCount;
    else if (event.key === 'PageDown') target = activeIndex + visibleCount;
    else if (event.key === 'Home') target = 0;
    else if (event.key === 'End') target = laneEvents.length - 1;
    else if (event.key === 'Enter' || event.key === ' ') target = activeIndex;
    else return;
    event.preventDefault();
    selectIndex(target);
  };

  /** The pointer's x from the plot's left end in pixels, unclamped, so a grip in the inset still counts. */
  const pointerPlotX = (clientX: number) => {
    const rect = minimapRef.current?.getBoundingClientRect();
    const width = rect && rect.width > 0 ? rect.width : stripWidth;
    return ((clientX - (rect?.left ?? 0)) / width) * stripWidth - overviewPlotInset;
  };
  const plotXToRatio = (plotXValue: number) => Math.min(1, Math.max(0, plotXValue / plotWidth));
  /** The brush covers the page events from the first lane event in view to the last one. */
  const brushSpan = () => {
    const pageStart = lanePageIndex(windowStart);
    const pageEnd = Math.min(pageEvents.length, lanePageIndex(windowStart + visibleCount - 1) + 1);
    return brushPixelSpan(overviewAxis, pageStart, pageEnd - pageStart, plotWidth, overviewMinBrushWidth);
  };
  const isOverBrush = (plotXValue: number) => {
    const { left, right } = brushSpan();
    return plotXValue >= left - overviewBrushGripReach && plotXValue <= right + overviewBrushGripReach;
  };
  const isOverBrushGrip = (plotXValue: number) => {
    const { left, right } = brushSpan();
    return Math.abs(plotXValue - left) <= overviewBrushGripReach || Math.abs(plotXValue - right) <= overviewBrushGripReach;
  };
  /** The bar under the pointer; on a short page, the nearest event's bar within reach. */
  const bucketAtRatio = (ratio: number): number | null => {
    if (!isFewEvents) {
      const bucketIndex = Math.min(bucketCount - 1, Math.floor(ratio * bucketCount));
      return overviewBuckets[bucketIndex]?.count ? bucketIndex : null;
    }
    // Edges sit halfway between neighbors, so the event whose span holds the pointer is the nearest one.
    const nearest = Math.min(pageEvents.length - 1, Math.floor(ratioToEventIndex(overviewAxis, ratio)));
    const distance = Math.abs(overviewAxis.positions[nearest] - ratio) * plotWidth;
    if (distance > fewEventBarWidth / 2 + 3) return null;
    const bucketIndex = overviewBuckets.findIndex((bucket) => bucket.firstEventIndex <= nearest && bucket.lastEventIndex >= nearest);
    return bucketIndex >= 0 ? bucketIndex : null;
  };

  // Pressing inside the brush drags it from where it was grabbed; pressing outside jumps there first.
  const handleMinimapPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    isDraggingMinimap.current = true;
    setHoveredBucket(null);
    event.currentTarget.setPointerCapture?.(event.pointerId);
    const pointerX = pointerPlotX(event.clientX);
    const isGripPress = maxStart > 0 && isOverBrushGrip(pointerX);
    let start = windowStart;
    // A lane overview shows only what is in view, so a press selects; the track below moves the view.
    if (!isLaneOverview && !isOverBrush(pointerX)) {
      start = clampStart(pageIndexToLaneIndex(laneToPage, ratioToEventIndex(overviewAxis, plotXToRatio(pointerX))) - visibleCount / 2);
      moveWindow(start);
    }
    // The real window edge, not the widened brush edge, so a press never moves the view.
    brushDragOrigin.current = {
      windowStart: start,
      pointerX,
      windowLeftX: eventIndexToRatio(overviewAxis, lanePageIndex(start)) * plotWidth,
      hasDragged: false,
      isGripPress,
    };
  };

  const handleMinimapPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const pointerX = pointerPlotX(event.clientX);
    if (isDraggingMinimap.current) {
      if (isLaneOverview) return;
      const origin = brushDragOrigin.current;
      if (Math.abs(pointerX - origin.pointerX) > overviewClickSlop) origin.hasDragged = true;
      // Back at the press point, restore the exact start; the pixel round trip may differ by float noise.
      moveWindow(pointerX === origin.pointerX
        ? origin.windowStart
        : pageIndexToLaneIndex(laneToPage, ratioToEventIndex(overviewAxis, plotXToRatio(origin.windowLeftX + pointerX - origin.pointerX))));
      return;
    }
    const bucketIndex = bucketAtRatio(plotXToRatio(pointerX));
    setHoveredBucket(bucketIndex);
    // Over a bar the cursor offers the click; the grips and empty brush space offer the drag.
    setIsPointerOverBrush(maxStart > 0 && (isOverBrushGrip(pointerX) || (bucketIndex === null && isOverBrush(pointerX))));
  };

  const endMinimapDrag = () => {
    isDraggingMinimap.current = false;
  };

  // A press that never became a drag is a click; on a bar it selects that bar's event. A grip press only drags.
  const handleMinimapPointerUp = () => {
    const isClick = isDraggingMinimap.current && !brushDragOrigin.current.hasDragged && !brushDragOrigin.current.isGripPress;
    endMinimapDrag();
    if (!isClick) return;
    const bucketIndex = bucketAtRatio(plotXToRatio(brushDragOrigin.current.pointerX));
    if (bucketIndex === null) return;
    const eventIndex = overviewBucketSelectionIndex(overviewBuckets[bucketIndex], statusList, retriedFlags, pageLaneFlags);
    if (eventIndex < 0) return;
    const laneIndex = indexByEventId.get(pageEvents[eventIndex].eventId);
    // A hidden event joins the lane once selected, and the selection effect reveals it.
    if (laneIndex !== undefined) centerOn(laneIndex);
    selectEvent(pageEvents[eventIndex]);
  };

  const handleMinimapKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    let next: number;
    if (event.key === 'ArrowLeft') next = windowStart - 1;
    else if (event.key === 'ArrowRight') next = windowStart + 1;
    else if (event.key === 'PageUp') next = windowStart - visibleCount;
    else if (event.key === 'PageDown') next = windowStart + visibleCount;
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = maxStart;
    else if (event.key === 'Enter') {
      event.preventDefault();
      selectIndex(Math.min(laneEvents.length - 1, Math.floor(windowStart + Math.min(visibleCount, laneEvents.length) / 2)));
      return;
    } else return;
    event.preventDefault();
    moveWindow(next);
  };

  if (!pageEvents.length) return <div className="card empty-state"><h3>No semantic events loaded</h3></div>;

  const firstRendered = Math.max(0, Math.floor(windowStart) - overscanSlots);
  const lastRendered = Math.min(laneEvents.length - 1, Math.ceil(windowStart) + visibleCount + overscanSlots);
  const renderedEvents = laneEvents.slice(firstRendered, lastRendered + 1);
  const firstVisible: FlowHistoryEvent | undefined = laneEvents[Math.min(laneEvents.length - 1, Math.round(windowStart))];
  const lastVisible: FlowHistoryEvent | undefined = laneEvents[Math.min(laneEvents.length - 1, Math.round(windowStart) + visibleCount - 1)];
  const visibleRangeText = firstVisible && lastVisible ? `Events #${firstVisible.eventId} to #${lastVisible.eventId}` : 'No events shown';
  const xOf = (index: number) => (index - windowStart) * slotWidth + slotWidth / 2;
  const isEndInView = windowStart + visibleCount >= laneEvents.length - 0.5;
  const filteredKinds = filteredKindCount(filterOptions);
  const brushInPlot = brushSpan();
  const brush = { left: overviewPlotInset + brushInPlot.left, right: overviewPlotInset + brushInPlot.right };
  const pageViewStart = lanePageIndex(windowStart);
  const pageViewEnd = lanePageIndex(windowStart + visibleCount - 1) + 1;
  // The brush only appears when the page is wider than the strip; otherwise there is nothing to move.
  const hasBrush = !isLaneOverview && maxStart > 0;
  const isIndexInView = (index: number) => xOf(index) >= 0 && xOf(index) <= stripWidth;
  // The lane overview draws only what is in view; the whole-page overview marks every event.
  const isOverviewMarkShown = (index: number) => !isLaneOverview || isIndexInView(index);
  const barSlotWidth = plotWidth / bucketCount;
  const barWidth = isFewEvents ? fewEventBarWidth : Math.max(1, barSlotWidth - (barSlotWidth > 4 ? 2 : 1));
  /** A bar's horizontal center in chart pixels: its own event on a short page, its slice otherwise. */
  const barCenterX = (bucket: OverviewBucket, bucketIndex: number) => overviewPlotInset + (isFewEvents
    ? overviewAxis.positions[bucket.firstEventIndex] * plotWidth
    : (bucketIndex + 0.5) * barSlotWidth);
  const plotX = (index: number) => overviewPlotInset + overviewAxis.positions[index] * plotWidth;
  const maxBucketCount = overviewBuckets.reduce((max, bucket) => Math.max(max, bucket.count), 0);
  const problemCount = overviewBuckets.reduce((sum, bucket) => sum + bucket.failed + bucket.retried, 0);
  const [axisStartLabel, axisEndLabel] = overviewAxisEnds(overviewAxis, pageEvents, timezone);
  const axisSpanLabel = [
    overviewAxis.startMs !== null && overviewAxis.endMs !== null && overviewAxis.endMs > overviewAxis.startMs
      ? formatElapsedDuration(overviewAxis.endMs - overviewAxis.startMs)
      : '',
    overviewAxis.kind === 'index' ? 'spaced by event order' : '',
  ].filter(Boolean).join(' · ');
  const hovered = hoveredBucket !== null && overviewBuckets[hoveredBucket]?.count
    ? overviewBucketLines(
      overviewAxis,
      overviewBuckets[hoveredBucket],
      hoveredBucket,
      bucketCount,
      pageEvents,
      timezone,
      bucketHiddenCounts[hoveredBucket],
    )
    : null;
  const hoveredSelectionIndex = hovered && hoveredBucket !== null
    ? overviewBucketSelectionIndex(overviewBuckets[hoveredBucket], statusList, retriedFlags, pageLaneFlags)
    : -1;
  const hoveredCenterRatio = hoveredBucket === null || !overviewBuckets[hoveredBucket]?.count
    ? 0
    : barCenterX(overviewBuckets[hoveredBucket], hoveredBucket) / stripWidth;

  return (
    <div className="timeline-wrap htl">
      {prunedEventIdRange !== undefined && pageEvents[0]?.eventId === 1 && (
        <div className="warning-banner timeline-pruned-banner" role="note">
          {prunedEventIdRange.firstEventId === prunedEventIdRange.lastEventId
            ? `Event #${prunedEventIdRange.firstEventId} was`
            : `Events #${prunedEventIdRange.firstEventId} to #${prunedEventIdRange.lastEventId} were`} removed by the history window; FlowStart is kept.
        </div>
      )}

      <div className="htl-head">
        <span className="htl-head-range">{axisStartLabel} → {axisEndLabel}</span>
        <span className="htl-overview-summary">
          {maxStart > 0 && firstVisible && lastVisible && (
            <span className="htl-in-view">#{firstVisible.eventId}–#{lastVisible.eventId} in view · </span>
          )}
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
        <span className="htl-head-tools">
          <span aria-live="polite" className="htl-filter-note-region">
            {hiddenEventCount > 0 && (
              <span className="htl-filter-note">
                {hiddenEventCount} of {pageEvents.length} events hidden on this page
                <button className="htl-filter-show-all" onClick={() => changeEventFilter(allTimelineEventsFilter)} type="button">
                  Show all
                </button>
              </span>
            )}
          </span>
          {shownEndNote && !isEndInView && (
            <button
              aria-label="Go to the newest event"
              className="htl-end-jump"
              onClick={() => moveWindow(maxStart)}
              title="Go to the newest event"
              type="button"
            >
              <span aria-hidden="true" className="live-status-dot" />
              Now
              <ChevronRight aria-hidden="true" size={13} strokeWidth={2.25} />
            </button>
          )}
          <TimelineLabelModeTabs
            introId={isLabelModeIntroShown ? labelModeIntroId : undefined}
            labelMode={labelMode}
            onChangeLabelMode={changeLabelMode}
          />
          <TimelineEventFilterMenu
            filter={eventFilter}
            filteredKindCount={filteredKinds}
            flowType={flowType}
            onChangeFilter={changeEventFilter}
            options={filterOptions}
          />
          <EventKindLegend />
        </span>
      </div>

      {isLabelModeIntroShown && <TimelineLabelModeIntro id={labelModeIntroId} onDismiss={dismissLabelModeIntro} />}

      <div
        aria-describedby={overviewHelpId}
        aria-label="Timeline overview"
        aria-valuemax={maxStart}
        aria-valuemin={0}
        aria-valuenow={Math.round(windowStart)}
        aria-valuetext={visibleRangeText}
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
        onPointerUp={handleMinimapPointerUp}
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
            if (!bucket.count || (isLaneOverview && !isIndexInView(bucket.firstEventIndex))) return [];
            const segments = overviewBarSegments(bucket, maxBucketCount);
            const centerX = barCenterX(bucket, bucketIndex);
            const x = centerX - barWidth / 2;
            const isInView = bucket.lastEventIndex >= Math.floor(pageViewStart) && bucket.firstEventIndex < pageViewEnd;
            const hasFailure = bucket.failed + bucket.retried > 0;
            const bandWidth = Math.max(barSlotWidth, barWidth + 4);
            const hiddenCount = bucketHiddenCounts[bucketIndex] ?? 0;
            let y = overviewHeight - segments.reduce((sum, segment) => sum + segment.height, 0);
            return [(
              <g
                className={[
                  'htl-bar',
                  isInView && hasBrush ? 'is-in-view' : '',
                  hoveredBucket === bucketIndex ? 'is-hovered' : '',
                  hasFailure ? 'has-failure' : '',
                  hiddenCount > 0 ? 'has-hidden' : '',
                ].filter(Boolean).join(' ')}
                data-count={bucket.count}
                data-hidden={hiddenCount || undefined}
                key={bucketIndex}
                // A bar fades with the share of its events the filter hides.
                style={hiddenCount > 0 ? { '--htl-bar-shown-share': (bucket.count - hiddenCount) / bucket.count } as CSSProperties : undefined}
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
            const pageIndex = pageIndexByEventId.get(eventId);
            if (index === undefined || pageIndex === undefined || index === selectedIndex || !isOverviewMarkShown(index)) return [];
            const x = plotX(pageIndex);
            return [<line className="htl-overview-linked" key={eventId} x1={x} x2={x} y1={overviewMarkerLaneHeight} y2={overviewHeight} />];
          })}
          {selectedIndex >= 0 && selectedPageIndex >= 0 && isOverviewMarkShown(selectedIndex) && (
            <g className="htl-overview-selection">
              <line x1={plotX(selectedPageIndex)} x2={plotX(selectedPageIndex)} y1={overviewMarkerLaneHeight - 2} y2={overviewHeight} />
              <circle cx={plotX(selectedPageIndex)} cy={overviewMarkerLaneHeight - 2} r={2.5} />
            </g>
          )}
          {hasBrush && (
            <g className="htl-overview-brush">
              {/* The page outside the window is dimmed, so the window reads as the part of the lane in view. */}
              <rect className="htl-overview-outside" height={overviewHeight} width={Math.max(0, brush.left)} x={0} y={0} />
              <rect
                className="htl-overview-outside"
                height={overviewHeight}
                width={Math.max(0, stripWidth - brush.right)}
                x={brush.right}
                y={0}
              />
              <rect className="htl-overview-brush-fill" height={overviewHeight - 1} rx={3} width={brush.right - brush.left} x={brush.left} y={0.5} />
              {[brush.left, brush.right].map((edge, edgeIndex) => (
                <g className="htl-overview-brush-edge" key={edgeIndex}>
                  {/* Grips sit just outside the brush, in the inset at either end, so they never cover a bar in view. */}
                  <rect height={16} rx={2} width={6} x={edgeIndex === 0 ? edge - 7 : edge + 1} y={(overviewHeight - 16) / 2} />
                  <line x1={edgeIndex === 0 ? edge - 4 : edge + 4} x2={edgeIndex === 0 ? edge - 4 : edge + 4} y1={(overviewHeight - 8) / 2} y2={(overviewHeight + 8) / 2} />
                </g>
              ))}
            </g>
          )}
        </svg>
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
            {hovered.hidden && <span className="htl-overview-tooltip-hidden">{hovered.hidden}</span>}
            {hoveredSelectionIndex >= 0 && !isPointerOverBrush && (
              <span className="htl-overview-tooltip-hint">Click to select #{pageEvents[hoveredSelectionIndex].eventId}</span>
            )}
          </div>
        )}
      </div>

      <div className="htl-lane">
      <div
        aria-activedescendant={selectedEvent && selectedIndex >= firstRendered && selectedIndex <= lastRendered
          ? `htl-event-${selectedEvent.eventId}`
          : undefined}
        aria-describedby={helpId}
        aria-label="Timeline events, oldest on the left"
        className={`htl-strip is-${labelMode}${isFilterChanging ? ' is-filter-changing' : ''}`}
        data-label-mode={labelMode}
        style={{
          '--htl-slot-width': `${slotWidth}px`,
          '--htl-label-lines': isTidy ? labelMaxLines : undefined,
          '--htl-label-angle': isTidy ? undefined : `-${denseLabelAngleDegrees}deg`,
          '--htl-label-max-width': isTidy ? undefined : `${denseLabelMaxWidth}px`,
          height: `${stripHeight}px`,
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
          const isRetried = status !== 'failed' && retriedFlags[laneToPage[index]];
          const isFilteredOut = !pageShownFlags[laneToPage[index]];
          const classes = [
            'htl-event',
            `kind-${kind}`,
            isSelected ? 'selected' : '',
            linkedEventIds.has(event.eventId) && !isSelected ? 'linked' : '',
            highlightedEventId === event.eventId ? 'jump-highlighted' : '',
            isFilteredOut ? 'is-filtered-out' : '',
            isFilterChanging && !previousLaneEventIds.current.has(event.eventId) ? 'is-entering' : '',
          ].filter(Boolean).join(' ');
          return (
            <div
              aria-label={[
                eventTitle(event),
                subject !== eventTitle(event) ? subject : '',
                state.spoken,
                isRetried ? retriedSpoken(event, status) : '',
                isFilteredOut ? 'hidden by the filter, shown because it is selected' : '',
                `#${event.eventId}`,
              ].filter(Boolean).join(', ')}
              aria-selected={isSelected}
              className={classes}
              data-event-id={event.eventId}
              id={`htl-event-${event.eventId}`}
              // A mode switch remounts the labels, so they fade in at their new places.
              key={`${labelMode}-${event.eventId}`}
              onClick={() => selectIndex(index)}
              role="option"
              style={{ left: `${(index - windowStart) * slotWidth}px` }}
              title={`${subject} · #${event.eventId}${isFilteredOut ? ' · hidden by the filter, shown because it is selected' : ''}`}
            >
              {/* An event whose dot scrolled past the left edge shows no label, so no label is cut. */}
              {xOf(index) >= 0 && (
                <span aria-hidden="true" className="htl-label">
                  {isTidy ? labelWithBreakHints(labels[index], slotWidth - labelColumnGap) : labels[index]}
                </span>
              )}
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
            // A filtered lane skips event IDs, so it names every tenth lane slot instead.
            const isOnInterval = (isLaneFiltered ? index : event.eventId) % axisEventInterval === 0;
            if (!isSelected && (!isOnInterval || isNextToSelection)) return [];
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
            const depth = Math.min(arcBandHeight - 4, 10 + Math.abs(toIndex - fromIndex) * 2);
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
      {laneEvents.length === 0 && (
        <div className="htl-filter-empty" role="note">
          No events on this page match the filter.
          <button className="htl-filter-show-all" onClick={() => changeEventFilter(allTimelineEventsFilter)} type="button">
            Show all
          </button>
        </div>
      )}
      {shownEndNote && isEndInView && laneEvents.length > 0 && (
        <TimelineEndNoteButton
          endNote={shownEndNote}
          left={xOf(laneEvents.length - 1) + 18}
          onShowEvent={onShowEndNoteEvent}
        />
      )}
      </div>

      <p className="sr-only" id={helpId}>
        Left and Right, or J and K, move the selection; Page Up and Page Down move by a screen. T time travels from the selected event. A sideways swipe or Shift with the wheel scrolls the lane.
      </p>
      <p className="sr-only" id={overviewHelpId}>
        Left, Right, Page Up, Page Down, Home, and End move the view; Enter selects the event at the center of the brush.
      </p>
    </div>
  );
}

/** The run's end note: a few words that select the event that says so. */
function TimelineEndNoteButton({
  endNote,
  left,
  onShowEvent,
}: {
  endNote: TimelineEndNote;
  left: number;
  onShowEvent?: (eventId: number) => void;
}) {
  return (
    <button
      className={`htl-end-note${endNote.isOpen ? ' is-open-run' : ''}`}
      disabled={!onShowEvent}
      onClick={() => onShowEvent?.(endNote.eventId)}
      style={{ left: `${left}px` }}
      title={`Show event #${endNote.eventId}`}
      type="button"
    >
      <span aria-hidden="true" className="htl-end-note-mark" />
      {endNote.label}
    </button>
  );
}
