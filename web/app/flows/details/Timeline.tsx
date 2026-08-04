// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import type { FlowHistoryEvent } from '@/lib/types';
import { formatDate } from '@/lib/format';
import { durabilityLabel } from '@/lib/semantic';
import { buildTimelineStepLinks, formatElapsedDuration, newestTimelineEvents } from '@/lib/timeline';
import { usePreferences } from '../../providers';
import { eventTitle } from './EventDetails';

interface StepLinkPath {
  id: string;
  label: string;
  path: string;
  duration: string;
  durationX: number;
  durationY: number;
}

interface StepLinkLayout {
  width: number;
  height: number;
  paths: StepLinkPath[];
}

function eventTone(event: FlowHistoryEvent) {
  if (event.type.endsWith('Failed')) return 'failed';
  if (event.type === 'StepWaitForCompleted') return 'waiting';
  if (event.type.endsWith('Completed')) return 'completed';
  if (event.type === 'FlowClosed') return 'closed';
  return 'neutral';
}

function executionSummary(event: FlowHistoryEvent): string {
  const execution = event.payload.execution;
  if (!execution || typeof execution !== 'object') return '';
  const info = execution as Record<string, unknown>;
  return [info.stepType, info.stepExecutionId].filter(Boolean).join(' · ');
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
  const eventDots = useRef(new Map<number, HTMLSpanElement>());
  const orderedEvents = useMemo(() => newestTimelineEvents(events), [events]);
  const stepLinks = useMemo(() => buildTimelineStepLinks(events), [events]);
  const [stepLinkLayout, setStepLinkLayout] = useState<StepLinkLayout>({ width: 0, height: 0, paths: [] });
  const updateStepLinks = useCallback(() => {
    const timeline = timelineRef.current;
    if (!timeline) return;
    const timelineRect = timeline.getBoundingClientRect();
    const paths = stepLinks.flatMap((link) => {
      const waitForDot = eventDots.current.get(link.waitForEventId);
      const executeDot = eventDots.current.get(link.executeEventId);
      if (!waitForDot || !executeDot) return [];
      const waitForRect = waitForDot.getBoundingClientRect();
      const executeRect = executeDot.getBoundingClientRect();
      const waitForX = waitForRect.right - timelineRect.left + 2;
      const executeX = executeRect.right - timelineRect.left + 2;
      const waitForY = waitForRect.top + waitForRect.height / 2 - timelineRect.top;
      const executeY = executeRect.top + executeRect.height / 2 - timelineRect.top;
      const linkX = Math.max(waitForX, executeX) + 15;
      const duration = formatElapsedDuration(link.conditionWaitDurationMs);
      return [{
        id: `${link.stepExecutionId}-${link.waitForEventId}-${link.executeEventId}`,
        label: `${link.stepExecutionId}: WaitForCondition started to Execute`,
        path: `M ${waitForX} ${waitForY} H ${linkX} V ${executeY} H ${executeX}`,
        duration,
        durationX: linkX + 6,
        durationY: (waitForY + executeY) / 2,
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
    eventDots.current.forEach((dot) => observer.observe(dot));
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
  return (
    <div className="timeline-wrap">
      <div className="view-toolbar">
        <div>
          <p className="eyebrow">Dex semantic history</p>
          <h2>{events.length} events</h2>
        </div>
      </div>
      <div className="timeline" ref={timelineRef}>
        {stepLinkLayout.paths.length > 0 && (
          <svg
            aria-hidden="true"
            className="timeline-step-links"
            height={stepLinkLayout.height}
            viewBox={`0 0 ${stepLinkLayout.width} ${stepLinkLayout.height}`}
            width={stepLinkLayout.width}
          >
            <defs>
              <marker id="timeline-step-arrow" markerHeight="7" markerWidth="7" orient="auto" refX="5" refY="3.5">
                <path d="M 0 0 L 6 3.5 L 0 7 z" />
              </marker>
            </defs>
            {stepLinkLayout.paths.map((link) => (
              <g key={link.id}>
                <path className="timeline-step-link" d={link.path} markerEnd="url(#timeline-step-arrow)">
                  <title>{link.label}</title>
                </path>
                {link.duration && (
                  <text
                    className="timeline-step-duration"
                    dominantBaseline="middle"
                    x={link.durationX}
                    y={link.durationY}
                  >
                    {link.duration}
                  </text>
                )}
              </g>
            ))}
          </svg>
        )}
        {orderedEvents.map((event) => {
          const eventMs = event.eventTime ? Date.parse(event.eventTime) : 0;
          const relative = startMs && eventMs ? Math.max(0, Math.round((eventMs - startMs) / 1000)) : null;
          const selected = selectedEvent?.eventId === event.eventId;
          const previousRunId = previousRunID(event);
          return (
            <article
              className={`timeline-row ${selected ? 'selected' : ''}`}
              key={event.eventId}
              onClick={() => onSelectEvent(event)}
            >
              <div className="timeline-time">
                <b>{formatDate(event.eventTime, timezone)}</b>
                {relative !== null && <span>+{relative}s</span>}
              </div>
              <div className="timeline-rail">
                <span
                  className={`timeline-dot tone-${eventTone(event)}`}
                  ref={(node) => {
                    if (node) eventDots.current.set(event.eventId, node);
                    else eventDots.current.delete(event.eventId);
                  }}
                />
              </div>
              <div className="event-card">
                <header>
                  <div>
                    <span className="event-id">#{event.eventId}</span>
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
                  <span className={`event-type tone-${eventTone(event)}`}>{event.type}</span>
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
  const execution = event.payload.execution as Record<string, unknown> | undefined;
  const failure = event.payload.failure as Record<string, unknown> | undefined;
  if (!execution && !failure) return null;
  return (
    <div className="event-highlights">
      {execution?.durability !== undefined && <span>Durability <b>{durabilityLabel(execution.durability)}</b></span>}
      {execution?.finalAttempt !== undefined && <span>Final attempt <b>{String(execution.finalAttempt)}</b></span>}
      {typeof failure?.message === 'string' && failure.message && (
        <span className="failure-message">{failure.message}</span>
      )}
    </div>
  );
}
