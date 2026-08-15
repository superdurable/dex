// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { CSSProperties } from 'react';
import { Link } from 'react-router-dom';
import type { FlowHistoryEvent } from '@/lib/types';
import { formatDate } from '@/lib/format';
import { durabilityLabel, flowErrorTypeLabel } from '@/lib/semantic';
import {
  buildSelectedTimelineLinks,
  displayEventNumber,
  formatElapsedDuration,
  newestTimelineEvents,
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

interface StepLinkLayout {
  width: number;
  height: number;
  paths: StepLinkPath[];
}

function eventTone(event: FlowHistoryEvent) {
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

function previousRunID(event: FlowHistoryEvent): string {
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
}: {
  flowId: string;
  events: FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
}) {
  const { timezone } = usePreferences();
  const timelineRef = useRef<HTMLDivElement>(null);
  const eventCards = useRef(new Map<number, HTMLDivElement>());
  const orderedEvents = useMemo(() => newestTimelineEvents(events), [events]);
  const eventNumbers = useMemo(() => new Map(
    events.map((event) => [event.eventId, displayEventNumber(events, event)]),
  ), [events]);
  const stepLinks = useMemo(
    () => buildSelectedTimelineLinks(events, selectedEvent?.eventId),
    [events, selectedEvent?.eventId],
  );
  const linkLaneCount = Math.max(1, ...stepLinks.map((link) => link.lane + 1));
  const timelineStyle = {
    '--timeline-link-rail-width': `${88 + (linkLaneCount - 1) * 48}px`,
  } as CSSProperties;
  const [stepLinkLayout, setStepLinkLayout] = useState<StepLinkLayout>({ width: 0, height: 0, paths: [] });
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
    setStepLinkLayout({ width: timelineRect.width, height: timelineRect.height, paths });
  }, [stepLinks]);

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
  return (
    <div className="timeline-wrap">
      <div className="view-toolbar">
        <div>
          <h2>{events.length} events</h2>
        </div>
      </div>
      <div className="timeline" ref={timelineRef} style={timelineStyle}>
        {stepLinkLayout.paths.length > 0 && (
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
          const previousRunId = previousRunID(event);
          return (
            <article
              className={`timeline-row${stepMethodClass(event)}${selected ? ' selected' : ''}${linked ? ' link-highlighted' : ''}${counterpart ? ' link-counterpart' : ''}`}
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
                    <span className="event-id">#{eventNumbers.get(event.eventId)}</span>
                    <h3>
                      {previousRunId ? (
                        <Link
                          className="event-run-link"
                          title={previousRunId}
                          to={`/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(previousRunId)}`}
                        >
                          Flow continued
                        </Link>
                      ) : eventTitle(event)}
                    </h3>
                    {executionSummary(event) && <p>{executionSummary(event)}</p>}
                  </div>
                  <span className={`event-type tone-${eventTone(event)}`}>{eventTypeLabel(event)}</span>
                </header>
                <EventHighlights event={event} />
              </div>
            </article>
          );
        })}
      </div>
    </div>
  );
}

function EventHighlights({ event }: { event: FlowHistoryEvent }) {
  const context = event.payload.context as Record<string, unknown> | undefined;
  const output = event.payload.output as Record<string, unknown> | undefined;
  const failure = output?.failure as Record<string, unknown> | undefined;
  const activityPhase = pendingPhase(event);
  if (!context && !failure && !activityPhase) return null;
  const backendError = typeof failure?.backendError === 'string'
    && failure.backendError.startsWith('FLOW_ERROR_TYPE_')
    ? flowErrorTypeLabel(failure.backendError)
    : failure?.backendError;
  return (
    <div className="event-highlights">
      {activityPhase && <span>Activity phase <b>{activityPhase}</b></span>}
      {context?.durability !== undefined && <span>Durability <b>{durabilityLabel(context.durability)}</b></span>}
      {context?.finalAttempt !== undefined && <span>Final attempt <b>{String(context.finalAttempt)}</b></span>}
      {typeof backendError === 'string' && backendError && (
        <span className="failure-message">{backendError}</span>
      )}
    </div>
  );
}
