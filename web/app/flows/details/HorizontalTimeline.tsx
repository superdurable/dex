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
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from 'react';
import type { EventIdRange } from '@/lib/historyRelations';
import {
  buildSelectedTimelineRelations,
  prunedRelatedEventTooltip,
  timelineRelatedEventLabel,
  type TimelineSelectedRelations,
} from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import { eventTitle } from './EventDetails';
import { eventTone, executionSummary, type TimelinePaging } from './Timeline';

/** Horizontal space per event; slanted labels let neighbors share it like angled parking. */
const slotWidth = 34;
/** Room at the right edge for the slanted labels of the newest visible events. */
const labelRunWidth = 200;
const overscanSlots = 6;
const fallbackStripWidth = 1200;
const arcBandHeight = 44;

/** Shows a Timeline page left to right with slanted labels; only events in view render. */
export function HorizontalTimeline({
  events,
  selectedEvent,
  onSelectEvent,
  relations,
  paging,
  prunedEventIdRange,
  highlightedEventId = null,
  onJumpToEvent,
}: {
  events: FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
  relations?: TimelineSelectedRelations;
  paging?: TimelinePaging;
  prunedEventIdRange?: EventIdRange;
  highlightedEventId?: number | null;
  onJumpToEvent?: (eventId: number) => void;
}) {
  const chronological = useMemo(() => [...events].sort((left, right) => left.eventId - right.eventId), [events]);
  const indexByEventId = useMemo(
    () => new Map(chronological.map((event, index) => [event.eventId, index])),
    [chronological],
  );
  const stripRef = useRef<HTMLDivElement>(null);
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

  // A new page, or Live adding events, keeps the newest end in view unless the reader moved away from it.
  useEffect(() => {
    const target = highlightedEventId ?? selectedEvent?.eventId;
    const targetIndex = target === undefined ? undefined : indexByEventId.get(target);
    if (targetIndex !== undefined && highlightedEventId !== null) {
      centerOn(targetIndex);
      return;
    }
    if (isAtNewestEnd.current) setWindowStart(maxStart);
    else setWindowStart((current) => Math.min(maxStart, Math.max(0, current)));
    // Runs when the page changes; selection changes recenter below.
  }, [chronological, maxStart, highlightedEventId]);

  const selectedIndex = selectedEvent ? indexByEventId.get(selectedEvent.eventId) ?? -1 : -1;
  useEffect(() => {
    if (selectedIndex < 0) return;
    if (selectedIndex < windowStart || selectedIndex >= windowStart + visibleCount) centerOn(selectedIndex);
    // Only a selection change recenters, so dragging the overview never snaps back.
  }, [selectedIndex]);

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
    if (event) onSelectEvent(event);
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

  const handleWheel = (event: ReactWheelEvent<HTMLDivElement>) => {
    const delta = Math.abs(event.deltaX) > Math.abs(event.deltaY) ? event.deltaX : event.deltaY;
    if (delta === 0) return;
    moveWindow(windowStart + delta / slotWidth);
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
  const otherPageStubs = computedRelations.stubs;

  return (
    <div className="timeline-wrap htl">
      <div className="view-toolbar htl-toolbar">
        <h2>{events.length} events</h2>
        <span className="htl-range">
          Showing #{firstVisible.eventId}–#{lastVisible.eventId} of this page
        </span>
      </div>
      {prunedEventIdRange !== undefined && chronological[0]?.eventId === 1 && (
        <div className="warning-banner timeline-pruned-banner" role="note">
          {prunedEventIdRange.firstEventId === prunedEventIdRange.lastEventId
            ? `Event #${prunedEventIdRange.firstEventId} was`
            : `Events #${prunedEventIdRange.firstEventId} to #${prunedEventIdRange.lastEventId} were`} removed by the history window; FlowStart is kept.
        </div>
      )}

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
        <svg aria-hidden="true" preserveAspectRatio="none" viewBox={`0 0 ${chronological.length} 32`}>
          {chronological.map((event, index) => {
            const tone = eventTone(event);
            const isProminent = tone === 'failed' || tone === 'waiting' || tone === 'pending' || tone === 'fork';
            return (
              <rect
                className={`htl-tick tone-${tone}${linkedEventIds.has(event.eventId) ? ' is-linked' : ''}${index === selectedIndex ? ' is-selected' : ''}`}
                height={isProminent || index === selectedIndex ? 26 : 14}
                key={event.eventId}
                width={1}
                x={index}
                y={isProminent || index === selectedIndex ? 3 : 15}
              />
            );
          })}
          <rect className="htl-minimap-window" height={30} width={Math.min(visibleCount, chronological.length)} x={windowStart} y={1} />
        </svg>
      </div>

      <div
        aria-activedescendant={selectedEvent && selectedIndex >= 0 ? `htl-event-${selectedEvent.eventId}` : undefined}
        aria-label="Timeline events, oldest on the left"
        className="htl-strip"
        onKeyDown={handleStripKeyDown}
        onWheel={handleWheel}
        ref={stripRef}
        role="listbox"
        tabIndex={0}
      >
        {renderedEvents.map((event, offset) => {
          const index = firstRendered + offset;
          const tone = eventTone(event);
          const summary = executionSummary(event);
          const stepType = summary.split(' · ')[0];
          const isSelected = index === selectedIndex;
          const classes = [
            'htl-event',
            `tone-${tone}`,
            isSelected ? 'selected' : '',
            linkedEventIds.has(event.eventId) && !isSelected ? 'linked' : '',
            highlightedEventId === event.eventId ? 'jump-highlighted' : '',
          ].filter(Boolean).join(' ');
          return (
            <div
              aria-label={`#${event.eventId} ${eventTitle(event)}${summary ? `, ${summary}` : ''}`}
              aria-selected={isSelected}
              className={classes}
              data-event-id={event.eventId}
              id={`htl-event-${event.eventId}`}
              key={event.eventId}
              onClick={() => onSelectEvent(event)}
              role="option"
              style={{ left: `${(index - windowStart) * slotWidth}px` }}
              title={`#${event.eventId} ${eventTitle(event)}${summary ? ` · ${summary}` : ''}`}
            >
              <span aria-hidden="true" className="htl-label">
                <span className="htl-id">#{event.eventId}</span> {eventTitle(event)}{stepType ? ` · ${stepType}` : ''}
              </span>
              <span aria-hidden="true" className="htl-dot" />
            </div>
          );
        })}
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

      {otherPageStubs.length > 0 && (
        <div aria-label="Related events off this page" className="htl-stubs" role="group">
          {otherPageStubs.map((stub) => {
            const label = `${stub.direction === 'older' ? '←' : '→'} ${timelineRelatedEventLabel(stub)}`;
            const jumpEventId = stub.location === 'other-page' ? stub.eventId : undefined;
            if (jumpEventId !== undefined && onJumpToEvent) {
              return (
                <button
                  className={`timeline-edge-stub kind-${stub.kind}`}
                  disabled={paging?.isLoading ?? false}
                  key={stub.id}
                  onClick={() => onJumpToEvent(jumpEventId)}
                  type="button"
                >
                  {label}
                </button>
              );
            }
            return (
              <span
                className={`timeline-edge-stub kind-${stub.kind} location-${stub.location}`}
                key={stub.id}
                title={stub.location === 'pruned' ? prunedRelatedEventTooltip(stub) : undefined}
              >
                {label}
              </span>
            );
          })}
        </div>
      )}
      <p className="htl-help">
        Left and Right move the selection; Page Up and Page Down move by a screen; drag the overview to look around.
      </p>
    </div>
  );
}
