// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties } from 'react';
import { Link } from 'react-router-dom';
import type { FlowHistoryEvent } from '@/lib/types';
import { formatDate } from '@/lib/format';
import type { EventIdRange } from '@/lib/historyRelations';
import { durabilityLabel, flowErrorTypeLabel } from '@/lib/semantic';
import { positiveRunAttempt, runAttemptSearch } from '@/lib/timeTravel';
import {
  buildSelectedTimelineRelations,
  formatElapsedDuration,
  formatTimelineOffset,
  newestTimelineEvents,
  PRUNED_RELATED_EVENT_EXPLANATION,
  prunedRelatedEventTooltip,
  rowsShowingAbsoluteTime,
  timelineRelatedEventStubLabel,
  type TimelineRelatedEvent,
  type TimelineSelectedRelations,
} from '@/lib/timeline';
import { usePreferences } from '../../providers';
import { eventTitle, eventTypeLabel } from './EventDetails';

interface StepLinkPath {
  id: string;
  kind: 'lineage' | 'condition-wait';
  stepExecutionId: string;
  label: string;
  lane: number;
  path: string;
  fromEventId: number;
  toEventId: number;
  duration: string;
  durationX: number;
  durationY: number;
  durationWidth: number;
}

interface StubLinkPath {
  id: string;
  kind: TimelineRelatedEvent['kind'];
  location: TimelineRelatedEvent['location'];
  path: string;
}

interface StepLinkLayout {
  width: number;
  height: number;
  paths: StepLinkPath[];
  stubPaths: StubLinkPath[];
}

/** Whether a paged Timeline has events on other pages; the run header pages through them. */
export interface TimelinePaging {
  hasOlder: boolean;
  hasNewer: boolean;
  isLoading: boolean;
}

export function eventTone(event: FlowHistoryEvent) {
  if (event.type === 'TimeTravelFork') return 'fork';
  if (event.type.endsWith('Pending')) return 'pending';
  if (event.type.endsWith('Failed')) return 'failed';
  if (event.type === 'StepWaitForCompleted') return 'waiting';
  if (event.type.endsWith('Completed')) return 'completed';
  if (event.type === 'FlowClosed') return 'closed';
  return 'neutral';
}

function stepMethodClass(event: FlowHistoryEvent) {
  if (event.type.startsWith('StepWaitFor')) return ' step-method-wait-for';
  if (event.type.startsWith('StepExecute')) return ' step-method-execute';
  return '';
}

export function executionSummary(event: FlowHistoryEvent): string {
  const context = event.payload.context;
  if (!context || typeof context !== 'object') return '';
  const info = context as Record<string, unknown>;
  return [info.stepType, info.stepExecutionId].filter(Boolean).join(' · ');
}

function pendingPhase(event: FlowHistoryEvent): string {
  if (!event.type.endsWith('Pending')) return '';
  if (event.payload.phase === 1) return 'Scheduled';
  if (event.payload.phase === 2) return 'Started';
  return 'Pending';
}

/** Where an event's previous-run link points: an earlier run attempt of this run, or another run. */
export function previousRunLink(flowId: string, event: FlowHistoryEvent): { search: string } | string | null {
  if (event.type === 'TimeTravelFork') {
    const previousRunAttempt = positiveRunAttempt(event.payload.previousRunAttempt);
    if (previousRunAttempt > 0) return { search: runAttemptSearch(previousRunAttempt) };
  }
  const previousRunId = previousRunID(event);
  return previousRunId
    ? `/v1/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(previousRunId)}`
    : null;
}

function previousRunID(event: FlowHistoryEvent): string {
  if (event.type !== 'TimeTravelFork') return '';
  return typeof event.payload.previousRunId === 'string' ? event.payload.previousRunId : '';
}

export function Timeline({
  flowId,
  events,
  selectedEvent,
  onSelectEvent,
  relations,
  paging,
  prunedEventIdRange,
  highlightedEventId = null,
  onJumpToEvent,
  runStepDurability,
}: {
  flowId: string;
  /** The run's configured Step durability; rows name a Step's durability only when it differs. */
  runStepDurability?: unknown;
  /** One page of events, at most one history window. */
  events: FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
  /** The selected event's related events; computed from this page when absent. */
  relations?: TimelineSelectedRelations;
  paging?: TimelinePaging;
  prunedEventIdRange?: EventIdRange;
  highlightedEventId?: number | null;
  onJumpToEvent?: (eventId: number) => void;
}) {
  const { timezone } = usePreferences();
  const timelineRef = useRef<HTMLDivElement>(null);
  const eventCards = useRef(new Map<number, HTMLDivElement>());
  const stubElements = useRef(new Map<string, HTMLElement>());
  const orderedEvents = useMemo(() => newestTimelineEvents(events), [events]);
  const absoluteTimeRows = useMemo(() => rowsShowingAbsoluteTime(orderedEvents), [orderedEvents]);
  const spansRunAttempts = useMemo(
    () => new Set(events.map((event) => positiveRunAttempt(event.runAttempt))).size > 1,
    [events],
  );
  const computedRelations = useMemo(
    () => relations ?? buildSelectedTimelineRelations(events, selectedEvent, {
      hasOlder: paging?.hasOlder ?? false,
      hasNewer: paging?.hasNewer ?? false,
      prunedEventIdRange,
    }),
    [events, paging?.hasNewer, paging?.hasOlder, prunedEventIdRange, relations, selectedEvent],
  );
  const stepLinks = computedRelations.links;
  const stubs = computedRelations.stubs;
  const linkLaneCount = Math.max(0, ...stepLinks.map((link) => link.lane + 1));
  const railLaneCount = Math.max(1, linkLaneCount + stubs.length);
  const timelineStyle = {
    '--timeline-link-rail-width': `${88 + (railLaneCount - 1) * 48}px`,
  } as CSSProperties;
  const [stepLinkLayout, setStepLinkLayout] = useState<StepLinkLayout>({
    width: 0,
    height: 0,
    paths: [],
    stubPaths: [],
  });
  const [interactionStepLinkID, setInteractionStepLinkID] = useState<string | null>(null);
  const [pinnedStepLinkID, setPinnedStepLinkID] = useState<string | null>(null);
  const updateStepLinks = useCallback(() => {
    const timeline = timelineRef.current;
    if (!timeline) return;
    const timelineRect = timeline.getBoundingClientRect();
    const paths = stepLinks.flatMap((link) => {
      const fromCard = eventCards.current.get(link.fromEventId);
      const toCard = eventCards.current.get(link.toEventId);
      if (!fromCard || !toCard) return [];
      const fromRect = fromCard.getBoundingClientRect();
      const toRect = toCard.getBoundingClientRect();
      const flowsUpward = fromRect.top > toRect.top;
      const fromX = fromRect.left - timelineRect.left - 1;
      const toX = toRect.left - timelineRect.left + 1;
      const fromY = fromRect.top + fromRect.height * (flowsUpward ? 0.34 : 0.66) - timelineRect.top;
      const toY = toRect.top + toRect.height * (flowsUpward ? 0.66 : 0.34) - timelineRect.top;
      const linkX = Math.min(fromX, toX) - 22 - link.lane * 16;
      const duration = formatElapsedDuration(link.elapsedDurationMs);
      const durationWidth = Math.max(28, duration.length * 7 + 10);
      return [{
        id: `${link.kind}-${link.stepExecutionId}-${link.fromEventId}-${link.toEventId}`,
        kind: link.kind,
        stepExecutionId: link.stepExecutionId,
        label: link.label,
        lane: link.lane,
        path: `M ${fromX} ${fromY} H ${linkX} V ${toY} H ${toX}`,
        fromEventId: link.fromEventId,
        toEventId: link.toEventId,
        duration,
        durationX: linkX - durationWidth,
        durationY: (fromY + toY) / 2,
        durationWidth,
      }];
    });
    const selectedCard = selectedEvent ? eventCards.current.get(selectedEvent.eventId) : undefined;
    const stubPaths = selectedCard ? stubs.flatMap((stub, offset) => {
      const stubElement = stubElements.current.get(stub.id);
      if (!stubElement) return [];
      const cardRect = selectedCard.getBoundingClientRect();
      const stubRect = stubElement.getBoundingClientRect();
      const isOlder = stub.direction === 'older';
      const cardX = cardRect.left - timelineRect.left;
      const cardY = cardRect.top + cardRect.height * (isOlder ? 0.66 : 0.34) - timelineRect.top;
      const stubX = stubRect.left - timelineRect.left;
      const stubY = stubRect.top + stubRect.height / 2 - timelineRect.top;
      const linkX = Math.min(cardX, stubX) - 22 - (linkLaneCount + offset) * 16;
      // History flows from older to newer events, so the arrow points at the newer end.
      const path = isOlder
        ? `M ${stubX - 1} ${stubY} H ${linkX} V ${cardY} H ${cardX + 1}`
        : `M ${cardX - 1} ${cardY} H ${linkX} V ${stubY} H ${stubX + 1}`;
      return [{ id: stub.id, kind: stub.kind, location: stub.location, path }];
    }) : [];
    setStepLinkLayout({ width: timelineRect.width, height: timelineRect.height, paths, stubPaths });
  }, [linkLaneCount, selectedEvent, stepLinks, stubs]);

  useLayoutEffect(() => {
    const timeline = timelineRef.current;
    if (!timeline) return;
    const frame = window.requestAnimationFrame(updateStepLinks);
    const observer = new ResizeObserver(updateStepLinks);
    observer.observe(timeline);
    eventCards.current.forEach((card) => observer.observe(card));
    window.addEventListener('resize', updateStepLinks);
    return () => {
      window.cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener('resize', updateStepLinks);
    };
  }, [orderedEvents, updateStepLinks]);

  useEffect(() => {
    if (highlightedEventId === null) return;
    eventCards.current.get(highlightedEventId)?.scrollIntoView?.({ block: 'center' });
  }, [highlightedEventId, orderedEvents]);

  const registerStub = useCallback((id: string, node: HTMLElement | null) => {
    if (node) stubElements.current.set(id, node);
    else stubElements.current.delete(id);
  }, []);

  const registerEventCard = useCallback((eventId: number, node: HTMLDivElement | null) => {
    if (node) eventCards.current.set(eventId, node);
    else eventCards.current.delete(eventId);
  }, []);

  const selectRow = useCallback((event: FlowHistoryEvent) => {
    setInteractionStepLinkID(null);
    setPinnedStepLinkID(null);
    onSelectEvent(event);
  }, [onSelectEvent]);

  if (!events.length) return <div className="card empty-state"><h3>No semantic events loaded</h3></div>;
  const startMs = events.reduce((earliest, event) => {
    const eventMs = event.eventTime ? Date.parse(event.eventTime) : 0;
    if (!eventMs) return earliest;
    return earliest ? Math.min(earliest, eventMs) : eventMs;
  }, 0);
  const selectedStepLink = stepLinkLayout.paths.find((link) => (
    link.fromEventId === selectedEvent?.eventId || link.toEventId === selectedEvent?.eventId
  ));
  const pinnedStepLink = stepLinkLayout.paths.find((link) => link.id === pinnedStepLinkID);
  const activeStepLinkID = interactionStepLinkID ?? pinnedStepLink?.id ?? selectedStepLink?.id ?? null;
  const activeStepLink = stepLinkLayout.paths.find((link) => link.id === activeStepLinkID);
  const firstEventId = orderedEvents[orderedEvents.length - 1].eventId;
  const showsPrunedBanner = prunedEventIdRange !== undefined && firstEventId === 1;
  const newerStubs = stubs.filter((stub) => stub.direction === 'newer');
  const olderStubs = stubs.filter((stub) => stub.direction === 'older');
  return (
    <div className="timeline-wrap">
      {showsPrunedBanner && (
        <div className="warning-banner timeline-pruned-banner" role="note" title={PRUNED_RELATED_EVENT_EXPLANATION}>
          {prunedRangeMessage(prunedEventIdRange)}
        </div>
      )}
      <div className="timeline" ref={timelineRef} style={timelineStyle}>
        <TimelineEdgeStubs
          edge="newer"
          isJumpDisabled={paging?.isLoading ?? false}
          stubs={newerStubs}
          onJumpToEvent={onJumpToEvent}
          registerStub={registerStub}
        />
        {(stepLinkLayout.paths.length > 0 || stepLinkLayout.stubPaths.length > 0) && (
          <svg
            aria-label="Step execution lineage and condition links"
            className="timeline-step-links"
            height={stepLinkLayout.height}
            role="group"
            viewBox={`0 0 ${stepLinkLayout.width} ${stepLinkLayout.height}`}
            width={stepLinkLayout.width}
          >
            <defs>
              <marker id="timeline-step-arrow" markerHeight="7" markerWidth="7" orient="auto" refX="5" refY="3.5">
                <path d="M 0 0 L 6 3.5 L 0 7 z" />
              </marker>
              <marker id="timeline-lineage-arrow" markerHeight="7" markerWidth="7" orient="auto" refX="5" refY="3.5">
                <path d="M 0 0 L 6 3.5 L 0 7 z" />
              </marker>
            </defs>
            {stepLinkLayout.stubPaths.map((stub) => (
              <path
                aria-hidden="true"
                className={`timeline-stub-link kind-${stub.kind} location-${stub.location}`}
                d={stub.path}
                key={stub.id}
                markerEnd={stub.kind === 'lineage' ? 'url(#timeline-lineage-arrow)' : 'url(#timeline-step-arrow)'}
              />
            ))}
            {stepLinkLayout.paths.map((link) => {
              const active = activeStepLinkID === link.id;
              const pinned = pinnedStepLinkID === link.id;
              const accessibleLabel = link.duration ? `${link.label}, ${link.duration}` : link.label;
              return (
                <g
                  aria-label={accessibleLabel}
                  aria-pressed={pinned}
                  className={`timeline-step-link-group kind-${link.kind}${active ? ' is-active' : ''}`}
                  data-step-execution-id={link.stepExecutionId}
                  data-timeline-link-kind={link.kind}
                  data-timeline-lane={link.lane}
                  key={link.id}
                  onBlur={() => setInteractionStepLinkID(null)}
                  onClick={() => setPinnedStepLinkID((current) => current === link.id ? null : link.id)}
                  onFocus={() => setInteractionStepLinkID(link.id)}
                  onKeyDown={(event) => {
                    if (event.key !== 'Enter' && event.key !== ' ') return;
                    event.preventDefault();
                    setPinnedStepLinkID((current) => current === link.id ? null : link.id);
                  }}
                  onMouseEnter={() => setInteractionStepLinkID(link.id)}
                  onMouseLeave={(event) => {
                    if (document.activeElement !== event.currentTarget) setInteractionStepLinkID(null);
                  }}
                  role="button"
                  tabIndex={0}
                >
                  <title>{accessibleLabel}</title>
                  <path aria-hidden="true" className="timeline-step-link-hitarea" d={link.path} />
                  <path
                    aria-hidden="true"
                    className="timeline-step-link"
                    d={link.path}
                    markerEnd={link.kind === 'lineage'
                      ? 'url(#timeline-lineage-arrow)'
                      : 'url(#timeline-step-arrow)'}
                  />
                  {link.duration && (
                    <>
                      <rect
                        aria-hidden="true"
                        className="timeline-step-duration-background"
                        height="20"
                        rx="7"
                        width={link.durationWidth}
                        x={link.durationX - 5}
                        y={link.durationY - 10}
                      />
                      <text
                        aria-hidden="true"
                        className="timeline-step-duration"
                        dominantBaseline="middle"
                        x={link.durationX}
                        y={link.durationY}
                      >
                        {link.duration}
                      </text>
                    </>
                  )}
                </g>
              );
            })}
          </svg>
        )}
        {orderedEvents.map((event, index) => {
          const linked = activeStepLink?.fromEventId === event.eventId || activeStepLink?.toEventId === event.eventId;
          const selected = selectedEvent?.eventId === event.eventId;
          const absoluteTime = formatDate(event.eventTime, timezone);
          const isNewAbsoluteTime = absoluteTimeRows[index];
          return (
            <TimelineRow
              absoluteTime={absoluteTime}
              isNewAbsoluteTime={isNewAbsoluteTime}
              runStepDurability={runStepDurability}
              counterpart={activeStepLink?.id === selectedStepLink?.id && linked && !selected}
              event={event}
              flowId={flowId}
              highlighted={highlightedEventId === event.eventId}
              key={event.eventId}
              linked={linked}
              onSelect={selectRow}
              registerEventCard={registerEventCard}
              selected={selected}
              showRunAttempt={spansRunAttempts}
              startMs={startMs}
            />
          );
        })}
        <TimelineEdgeStubs
          edge="older"
          isJumpDisabled={paging?.isLoading ?? false}
          stubs={olderStubs}
          onJumpToEvent={onJumpToEvent}
          registerStub={registerStub}
        />
      </div>
    </div>
  );
}

/** Memoized so a selection or link hover re-renders only the rows it changes, not the whole page. */
const TimelineRow = memo(function TimelineRow({
  event,
  flowId,
  absoluteTime,
  isNewAbsoluteTime,
  runStepDurability,
  startMs,
  selected,
  linked,
  counterpart,
  highlighted,
  showRunAttempt,
  onSelect,
  registerEventCard,
}: {
  event: FlowHistoryEvent;
  flowId: string;
  /** The formatted event time; rows show it only when it differs from the row above. */
  absoluteTime: string;
  isNewAbsoluteTime: boolean;
  runStepDurability: unknown;
  startMs: number;
  selected: boolean;
  linked: boolean;
  counterpart: boolean;
  highlighted: boolean;
  showRunAttempt: boolean;
  onSelect: (event: FlowHistoryEvent) => void;
  registerEventCard: (eventId: number, node: HTMLDivElement | null) => void;
}) {
  const eventMs = event.eventTime ? Date.parse(event.eventTime) : 0;
  const relative = startMs && eventMs ? Math.max(0, Math.round((eventMs - startMs) / 1000)) : null;
  const previousRunId = previousRunID(event);
  const previousRunTarget = previousRunLink(flowId, event);
  return (
    <article
      className={`timeline-row${stepMethodClass(event)}${selected ? ' selected' : ''}${linked ? ' link-highlighted' : ''}${counterpart ? ' link-counterpart' : ''}${highlighted ? ' jump-highlighted' : ''}`}
      data-event-id={event.eventId}
      onClick={() => onSelect(event)}
    >
      <div className="timeline-time" title={absoluteTime}>
        {isNewAbsoluteTime ? <b>{absoluteTime}</b> : <b className="sr-only">{absoluteTime}</b>}
        {relative !== null && <span>+{formatTimelineOffset(relative)}</span>}
      </div>
      <div className="timeline-rail">
        <span
          className={`timeline-dot tone-${eventTone(event)}${linked ? ' link-highlighted' : ''}`}
        />
      </div>
      <div className="event-card" ref={(node) => registerEventCard(event.eventId, node)}>
        <header>
          <div>
            <span className="event-id">#{event.eventId}</span>
            <h3 title={eventTypeLabel(event)}>
              {previousRunTarget ? (
                <Link
                  className="event-run-link"
                  title={previousRunId}
                  to={previousRunTarget}
                >
                  {eventTitle(event)}
                </Link>
              ) : eventTitle(event)}
            </h3>
            {executionSummary(event) && <p>{executionSummary(event)}</p>}
          </div>
        </header>
        <EventHighlights event={event} runStepDurability={runStepDurability} showRunAttempt={showRunAttempt} />
      </div>
    </article>
  );
});

/** Ends links whose far end is on another page, or was pruned, at the top or bottom edge. */
function TimelineEdgeStubs({
  edge,
  isJumpDisabled,
  stubs,
  onJumpToEvent,
  registerStub,
}: {
  edge: 'newer' | 'older';
  isJumpDisabled: boolean;
  stubs: TimelineRelatedEvent[];
  onJumpToEvent?: (eventId: number) => void;
  registerStub: (id: string, node: HTMLElement | null) => void;
}) {
  if (!stubs.length) return null;
  return (
    <div className={`timeline-edge-stubs edge-${edge}`}>
      <div className="timeline-edge-stub-list">
        {stubs.map((stub) => {
          // Newest-first order puts older stubs at the bottom edge and newer ones at the top.
          const label = `${edge === 'older' ? '↓' : '↑'} ${timelineRelatedEventStubLabel(stub)}`;
          const jumpEventId = stub.location === 'other-page' ? stub.eventId : undefined;
          if (jumpEventId !== undefined && onJumpToEvent) {
            return (
              <button
                className={`timeline-edge-stub kind-${stub.kind}`}
                data-related-event-location={stub.location}
                disabled={isJumpDisabled}
                key={stub.id}
                onClick={() => onJumpToEvent(jumpEventId)}
                ref={(node) => registerStub(stub.id, node)}
                title={`Load the page with event #${jumpEventId}`}
                type="button"
              >
                {label}
              </button>
            );
          }
          return (
            <span
              className={`timeline-edge-stub kind-${stub.kind} location-${stub.location}`}
              data-related-event-location={stub.location}
              key={stub.id}
              ref={(node) => registerStub(stub.id, node)}
              title={stub.location === 'pruned' ? prunedRelatedEventTooltip(stub) : undefined}
            >
              {label}
              {stub.location === 'pruned' && <span className="timeline-pruned-badge">Pruned</span>}
            </span>
          );
        })}
      </div>
    </div>
  );
}

function prunedRangeMessage(range: EventIdRange): string {
  const removed = range.firstEventId === range.lastEventId
    ? `Event #${range.firstEventId} was`
    : `Events #${range.firstEventId} to #${range.lastEventId} were`;
  return `${removed} removed by the history window; FlowStart is kept.`;
}

/** An unset or unspecified run durability means the server default, sync. */
function effectiveRunStepDurability(value: unknown): unknown {
  const isUnset = value === undefined || value === null || value === 0 || value === 'STEP_DURABILITY_UNSPECIFIED';
  return isUnset ? 1 : value;
}

function EventHighlights({
  event,
  runStepDurability,
  showRunAttempt,
}: {
  event: FlowHistoryEvent;
  runStepDurability: unknown;
  showRunAttempt: boolean;
}) {
  const context = event.payload.context as Record<string, unknown> | undefined;
  const output = event.payload.output as Record<string, unknown> | undefined;
  const failure = output?.failure as Record<string, unknown> | undefined;
  const activityPhase = pendingPhase(event);
  const runAttempt = showRunAttempt ? positiveRunAttempt(event.runAttempt) : 0;
  const durability = context?.durability !== undefined ? durabilityLabel(context.durability) : '';
  const isDurabilityOverride = durability !== '' && durability !== durabilityLabel(effectiveRunStepDurability(runStepDurability));
  const finalAttempt = Number(context?.finalAttempt);
  const isRetried = Number.isFinite(finalAttempt) && finalAttempt > 1;
  if (!failure && !activityPhase && runAttempt === 0 && !isDurabilityOverride && !isRetried) return null;
  const backendError = typeof failure?.backendError === 'string'
    && failure.backendError.startsWith('FLOW_ERROR_TYPE_')
    ? flowErrorTypeLabel(failure.backendError)
    : failure?.backendError;
  return (
    <div className="event-highlights">
      {runAttempt > 0 && <span>Run attempt <b>{runAttempt}</b></span>}
      {activityPhase && <span>Activity phase <b>{activityPhase}</b></span>}
      {isDurabilityOverride && <span>Durability <b>{durability}</b></span>}
      {isRetried && <span>Attempt <b>{finalAttempt}</b></span>}
      {typeof backendError === 'string' && backendError && (
        <span className="failure-message">{backendError}</span>
      )}
    </div>
  );
}
