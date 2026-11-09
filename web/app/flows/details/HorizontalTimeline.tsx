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
import type { EventIdRange } from '@/lib/historyRelations';
import {
  buildSelectedTimelineRelations,
  type TimelineSelectedRelations,
} from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  Flag,
  FlagOff,
  GitFork,
  Hourglass,
  Play,
  Send,
  Tag,
  Trash2,
  X,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import { flowStatusLabel } from '@/lib/semantic';
import { eventTitle } from './EventDetails';
import { executionSummary, type TimelinePaging } from './Timeline';

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
/** The overview track is 10 px; neutral ticks form a 4 px density line inside it. */
const minimapHeight = 10;
const neutralTickHeight = 4;

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

/** Only states that need attention get a status; everything else is normal. */
type EventStatus = 'normal' | 'waiting' | 'failed' | 'canceled';

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
      </ul>
    </div>
  );
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

  const moveWindowToPointer = (clientX: number) => {
    const rect = minimapRef.current?.getBoundingClientRect();
    const width = rect && rect.width > 0 ? rect.width : stripWidth;
    const ratio = (clientX - (rect?.left ?? 0)) / width;
    centerOn(ratio * chronological.length);
  };

  const handleMinimapPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    isDraggingMinimap.current = true;
    event.currentTarget.setPointerCapture?.(event.pointerId);
    moveWindowToPointer(event.clientX);
  };

  const handleMinimapPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (isDraggingMinimap.current) moveWindowToPointer(event.clientX);
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
  // Ticks stay about 3 px wide, so a short page does not read as solid blocks.
  const tickWidth = Math.min(1, (3 * chronological.length) / Math.max(1, stripWidth));
  // The view window stays at least 12 px wide, so a long page keeps it findable.
  const windowWidth = Math.min(
    chronological.length,
    Math.max(visibleCount, (12 * chronological.length) / Math.max(1, stripWidth)),
  );

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
        className="htl-minimap"
        onKeyDown={handleMinimapKeyDown}
        onLostPointerCapture={() => { isDraggingMinimap.current = false; }}
        onPointerDown={handleMinimapPointerDown}
        onPointerMove={handleMinimapPointerMove}
        onPointerUp={() => { isDraggingMinimap.current = false; }}
        ref={minimapRef}
        role="slider"
        tabIndex={0}
      >
        <svg aria-hidden="true" preserveAspectRatio="none" viewBox={`0 0 ${chronological.length} ${minimapHeight}`}>
          {chronological.map((event, index) => {
            const status = states.get(event.eventId)?.status ?? 'normal';
            const isProminent = status !== 'normal' || index === selectedIndex;
            const tickHeight = isProminent ? minimapHeight : neutralTickHeight;
            return (
              <rect
                className={`htl-tick htl-status-${status}${linkedEventIds.has(event.eventId) ? ' is-linked' : ''}${index === selectedIndex ? ' is-selected' : ''}`}
                height={tickHeight}
                key={event.eventId}
                width={tickWidth}
                x={index + (1 - tickWidth) / 2}
                y={(minimapHeight - tickHeight) / 2}
              />
            );
          })}
          <rect
            className="htl-minimap-window"
            height={minimapHeight - 1}
            width={windowWidth}
            x={Math.min(windowStart, chronological.length - windowWidth)}
            y={0.5}
          />
        </svg>
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
