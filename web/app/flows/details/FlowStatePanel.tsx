// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef } from 'react';
import { formatDate } from '@/lib/format';
import {
  formatTimelineOffset,
  prunedRelatedEventTooltip,
  timelineRelatedEventLabel,
  type TimelineRelatedEvent,
} from '@/lib/timeline';
import { usePreferences } from '../../providers';
import type { FlowHistoryEvent } from '@/lib/types';
import { EventDetails, eventTitle } from './EventDetails';

function canScroll(element: HTMLElement, deltaY: number): boolean {
  if (deltaY < 0) return element.scrollTop > 0;
  if (deltaY > 0) return element.scrollTop + element.clientHeight < element.scrollHeight;
  return false;
}

function containSidebarWheel(event: WheelEvent) {
  const sidebar = event.currentTarget;
  if (!(sidebar instanceof HTMLElement) || sidebar.scrollHeight <= sidebar.clientHeight) return;
  const nested = event.target instanceof Element
    ? event.target.closest<HTMLElement>('.raw-event-json, .semantic-value pre, .semantic-alert pre, .json-view pre, .json-view-raw')
    : null;
  if (nested && nested !== sidebar && canScroll(nested, event.deltaY)) return;
  if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;
  event.preventDefault();
  event.stopPropagation();
  const multiplier = event.deltaMode === WheelEvent.DOM_DELTA_LINE
    ? 16
    : event.deltaMode === WheelEvent.DOM_DELTA_PAGE
      ? sidebar.clientHeight
      : 1;
  sidebar.scrollTop += event.deltaY * multiplier;
}

export function FlowStatePanel({
  selectedEvent,
  history,
  parentFlowId,
  relatedEvents = [],
  onSelectRelatedEvent,
  onJumpToEvent,
  isJumpDisabled = false,
  runStartTime,
}: {
  selectedEvent: FlowHistoryEvent | null;
  /** The run's start time, for each event's offset from it. */
  runStartTime?: string | null;
  history: FlowHistoryEvent[];
  parentFlowId: string;
  relatedEvents?: TimelineRelatedEvent[];
  onSelectRelatedEvent?: (eventId: number) => void;
  onJumpToEvent?: (eventId: number) => void;
  /** True while a Timeline page loads, so a second jump cannot race it. */
  isJumpDisabled?: boolean;
}) {
  const sidebar = useRef<HTMLDivElement>(null);
  const { timezone } = usePreferences();
  const eventMs = selectedEvent?.eventTime ? Date.parse(selectedEvent.eventTime) : Number.NaN;
  const startMs = runStartTime ? Date.parse(runStartTime) : Number.NaN;
  const offset = Number.isFinite(eventMs) && Number.isFinite(startMs)
    ? `+${formatTimelineOffset((eventMs - startMs) / 1000)}`
    : '';

  useEffect(() => {
    const element = sidebar.current;
    if (!element) return;
    element.addEventListener('wheel', containSidebarWheel, { passive: false });
    return () => element.removeEventListener('wheel', containSidebarWheel);
  }, []);

  return (
    <div aria-label="Selected event details" className="sidebar-stack" ref={sidebar} role="region" tabIndex={0}>
      {selectedEvent ? (
        <>
          <header aria-label="Selected event" className="selected-event-anchor" data-selected-event-target>
            <h3>{eventTitle(selectedEvent)}</h3>
            <div className="event-meta">
              <span className="mono">#{selectedEvent.eventId}</span>
              <span title={selectedEvent.eventTime ?? undefined}>
                {selectedEvent.eventTime ? formatDate(selectedEvent.eventTime, timezone) : 'No timestamp'}
              </span>
              {offset && <span>{offset}</span>}
            </div>
          </header>
          {relatedEvents.length > 0 && (
            <RelatedEvents
              relatedEvents={relatedEvents}
              isJumpDisabled={isJumpDisabled}
              onJumpToEvent={onJumpToEvent}
              onSelectRelatedEvent={onSelectRelatedEvent}
            />
          )}
          <section className="sidebar-section selected-event-body">
            <EventDetails
              event={selectedEvent}
              history={history}
              parentFlowId={parentFlowId}
            />
          </section>
        </>
      ) : (
        <section className="sidebar-section">
          <h3>No event selected</h3>
          <p className="muted">Select an event from Timeline or Execution graph.</p>
        </section>
      )}
    </div>
  );
}

const relationTitles: Record<TimelineRelatedEvent['relation'], string> = {
  'wait-for': 'WaitFor',
  execute: 'Execute',
  source: 'Source execution',
  started: 'Started execution',
};

function RelatedEvents({
  relatedEvents,
  isJumpDisabled,
  onSelectRelatedEvent,
  onJumpToEvent,
}: {
  relatedEvents: TimelineRelatedEvent[];
  isJumpDisabled: boolean;
  onSelectRelatedEvent?: (eventId: number) => void;
  onJumpToEvent?: (eventId: number) => void;
}) {
  return (
    <section aria-label="Related events" className="sidebar-section related-events">
      <h4>Related events</h4>
      <ul>
        {relatedEvents.map((related) => (
          <li className={`related-event location-${related.location}`} key={related.id}>
            <span className="related-event-relation">{relationTitles[related.relation]}</span>
            <span className="related-event-label" title={timelineRelatedEventLabel(related)}>
              {timelineRelatedEventLabel(related)}
            </span>
            <RelatedEventAction
              related={related}
              isJumpDisabled={isJumpDisabled}
              onJumpToEvent={onJumpToEvent}
              onSelectRelatedEvent={onSelectRelatedEvent}
            />
          </li>
        ))}
      </ul>
    </section>
  );
}

function RelatedEventAction({
  related,
  isJumpDisabled,
  onSelectRelatedEvent,
  onJumpToEvent,
}: {
  related: TimelineRelatedEvent;
  isJumpDisabled: boolean;
  onSelectRelatedEvent?: (eventId: number) => void;
  onJumpToEvent?: (eventId: number) => void;
}) {
  const eventId = related.eventId;
  if (related.location === 'loaded' && eventId !== undefined && onSelectRelatedEvent) {
    return (
      <button className="button ghost related-event-action" onClick={() => onSelectRelatedEvent(eventId)} type="button">
        Show #{eventId}
      </button>
    );
  }
  if (related.location === 'other-page' && eventId !== undefined && onJumpToEvent) {
    // A jump loads a window around the event, so only the direction of its page is exact.
    const pageHint = related.direction === 'older' ? 'older page' : 'newer page';
    return (
      <span className="related-event-action related-event-jump">
        <button
          aria-describedby={`${related.id}-page-hint`}
          className="button ghost"
          disabled={isJumpDisabled}
          onClick={() => onJumpToEvent(eventId)}
          type="button"
        >
          Go to #{eventId}
        </button>
        <span className="related-event-hint" id={`${related.id}-page-hint`}>{pageHint}</span>
      </span>
    );
  }
  if (related.location === 'pruned') {
    return (
      <span className="related-event-action related-event-unavailable" title={prunedRelatedEventTooltip(related)}>
        Outside the retention window
      </span>
    );
  }
  if (related.location === 'loaded') return <span className="related-event-action related-event-unavailable">On this page</span>;
  return <span className="related-event-action related-event-unavailable">Not on this page</span>;
}
