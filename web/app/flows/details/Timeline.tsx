// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
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
  newestTimelineEvents,
  PRUNED_RELATED_EVENT_EXPLANATION,
  prunedRelatedEventTooltip,
  timelineRelatedEventLabel,
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

/** Older and newer page navigation for a paged Timeline. */
export interface TimelinePaging {
  hasOlder: boolean;
  hasNewer: boolean;
  /** Whether the page is the newest window, which Live extends. */
  isNewest: boolean;
  isLoading: boolean;
  canGoBack: boolean;
  onOlder: () => void;
  onNewer: () => void;
  onNewest: () => void;
  onBack: () => void;
}

function eventTone(event: FlowHistoryEvent) {
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

function executionSummary(event: FlowHistoryEvent): string {
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
  if (event.type === 'TimeTravelFork') {
    return typeof event.payload.previousRunId === 'string' ? event.payload.previousRunId : '';
  }
  if (event.type !== 'FlowStartedOrContinued') return '';
  const continued = event.payload.continuedStart;
  if (!continued || typeof continued !== 'object') return '';
  const value = (continued as Record<string, unknown>).previousRunId;
  return typeof value === 'string' ? value : '';
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
}: {
  flowId: string;
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
  const lastEventId = orderedEvents[0].eventId;
  const showsPrunedBanner = prunedEventIdRange !== undefined && firstEventId === 1;
  const newerStubs = stubs.filter((stub) => stub.direction === 'newer');
  const olderStubs = stubs.filter((stub) => stub.direction === 'older');
  return (
    <div className="timeline-wrap">
      <div className="view-toolbar">
        <div>
          <h2>{events.length} events</h2>
          {paging && (
            <p className="muted timeline-page-range">
              Events #{firstEventId} to #{lastEventId}{paging.isNewest ? ', newest page' : ''}
            </p>
          )}
        </div>
        {paging && <TimelinePager paging={paging} />}
      </div>
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
        {orderedEvents.map((event) => {
          const eventMs = event.eventTime ? Date.parse(event.eventTime) : 0;
          const relative = startMs && eventMs ? Math.max(0, Math.round((eventMs - startMs) / 1000)) : null;
          const selected = selectedEvent?.eventId === event.eventId;
          const linked = activeStepLink?.fromEventId === event.eventId || activeStepLink?.toEventId === event.eventId;
          const counterpart = activeStepLink?.id === selectedStepLink?.id && linked && !selected;
          const highlighted = highlightedEventId === event.eventId;
          const previousRunId = previousRunID(event);
          const previousRunTarget = previousRunLink(flowId, event);
          return (
            <article
              className={`timeline-row${stepMethodClass(event)}${selected ? ' selected' : ''}${linked ? ' link-highlighted' : ''}${counterpart ? ' link-counterpart' : ''}${highlighted ? ' jump-highlighted' : ''}`}
              data-event-id={event.eventId}
              key={event.eventId}
              onClick={() => {
                setInteractionStepLinkID(null);
                setPinnedStepLinkID(null);
                onSelectEvent(event);
              }}
            >
              <div className="timeline-time">
                <b>{formatDate(event.eventTime, timezone)}</b>
                {relative !== null && <span>+{relative}s</span>}
              </div>
              <div className="timeline-rail">
                <span
                  className={`timeline-dot tone-${eventTone(event)}${linked ? ' link-highlighted' : ''}`}
                />
              </div>
              <div
                className="event-card"
                ref={(node) => {
                  if (node) eventCards.current.set(event.eventId, node);
                  else eventCards.current.delete(event.eventId);
                }}
              >
                <header>
                  <div>
                    <span className="event-id">#{event.eventId}</span>
                    <h3>
                      {previousRunTarget ? (
                        <Link
                          className="event-run-link"
                          title={previousRunId}
                          to={previousRunTarget}
                        >
                          {event.type === 'TimeTravelFork' ? 'Time Travel fork' : 'Flow continued'}
                        </Link>
                      ) : eventTitle(event)}
                    </h3>
                    {executionSummary(event) && <p>{executionSummary(event)}</p>}
                  </div>
                  <span className={`event-type tone-${eventTone(event)}`}>{eventTypeLabel(event)}</span>
                </header>
                <EventHighlights event={event} showRunAttempt={spansRunAttempts} />
              </div>
            </article>
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

function TimelinePager({ paging }: { paging: TimelinePaging }) {
  return (
    <div aria-label="Timeline pages" className="timeline-pager" role="group">
      {paging.isLoading && <span className="muted">Loading…</span>}
      {paging.canGoBack && (
        <button className="button ghost" disabled={paging.isLoading} onClick={paging.onBack} type="button">
          Back
        </button>
      )}
      {!paging.isNewest && (
        <button className="button ghost" disabled={paging.isLoading} onClick={paging.onNewest} type="button">
          Newest
        </button>
      )}
      <button
        className="button ghost"
        disabled={paging.isLoading || paging.isNewest || !paging.hasNewer}
        onClick={paging.onNewer}
        type="button"
      >
        Newer events
      </button>
      <button
        className="button ghost"
        disabled={paging.isLoading || !paging.hasOlder}
        onClick={paging.onOlder}
        type="button"
      >
        Older events
      </button>
    </div>
  );
}

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
          const label = `${edge === 'older' ? '↓' : '↑'} ${timelineRelatedEventLabel(stub)}`;
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
              {stub.location === 'pruned' && <span className="timeline-pruned-badge">pruned</span>}
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

function EventHighlights({ event, showRunAttempt }: { event: FlowHistoryEvent; showRunAttempt: boolean }) {
  const context = event.payload.context as Record<string, unknown> | undefined;
  const output = event.payload.output as Record<string, unknown> | undefined;
  const failure = output?.failure as Record<string, unknown> | undefined;
  const activityPhase = pendingPhase(event);
  const runAttempt = showRunAttempt ? positiveRunAttempt(event.runAttempt) : 0;
  if (!context && !failure && !activityPhase && runAttempt === 0) return null;
  const backendError = typeof failure?.backendError === 'string'
    && failure.backendError.startsWith('FLOW_ERROR_TYPE_')
    ? flowErrorTypeLabel(failure.backendError)
    : failure?.backendError;
  return (
    <div className="event-highlights">
      {runAttempt > 0 && <span>Run attempt <b>{runAttempt}</b></span>}
      {activityPhase && <span>Activity phase <b>{activityPhase}</b></span>}
      {context?.durability !== undefined && <span>Durability <b>{durabilityLabel(context.durability)}</b></span>}
      {context?.finalAttempt !== undefined && <span>Final attempt <b>{String(context.finalAttempt)}</b></span>}
      {typeof backendError === 'string' && backendError && (
        <span className="failure-message">{backendError}</span>
      )}
    </div>
  );
}
