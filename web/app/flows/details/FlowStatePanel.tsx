// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef } from 'react';
import {
  prunedRelatedEventTooltip,
  timelineRelatedEventLabel,
  type TimelineRelatedEvent,
} from '@/lib/timeline';
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
}: {
  selectedEvent: FlowHistoryEvent | null;
  history: FlowHistoryEvent[];
  parentFlowId: string;
  relatedEvents?: TimelineRelatedEvent[];
  onSelectRelatedEvent?: (eventId: number) => void;
  onJumpToEvent?: (eventId: number) => void;
}) {
  const sidebar = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = sidebar.current;
    if (!element) return;
    element.addEventListener('wheel', containSidebarWheel, { passive: false });
    return () => element.removeEventListener('wheel', containSidebarWheel);
  }, []);

  return (
    <div className="sidebar-stack" ref={sidebar}>
      {selectedEvent ? (
        <>
          <header className="selected-event-anchor" data-selected-event-target>
            <p className="eyebrow">Selected event</p>
            <h3>{eventTitle(selectedEvent)}</h3>
            <div className="event-meta">
              <span>Event #{selectedEvent.eventId}</span>
              <span>{selectedEvent.eventTime || 'No timestamp'}</span>
            </div>
          </header>
          {relatedEvents.length > 0 && (
            <RelatedEvents
              relatedEvents={relatedEvents}
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
          <p className="eyebrow">Selected event</p>
          <h3>None</h3>
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
  onSelectRelatedEvent,
  onJumpToEvent,
}: {
  relatedEvents: TimelineRelatedEvent[];
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
            <span className="related-event-label">{timelineRelatedEventLabel(related)}</span>
            <RelatedEventAction
              related={related}
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
  onSelectRelatedEvent,
  onJumpToEvent,
}: {
  related: TimelineRelatedEvent;
  onSelectRelatedEvent?: (eventId: number) => void;
  onJumpToEvent?: (eventId: number) => void;
}) {
  const eventId = related.eventId;
  if (related.location === 'loaded' && eventId !== undefined && onSelectRelatedEvent) {
    return (
      <button className="button ghost related-event-action" onClick={() => onSelectRelatedEvent(eventId)} type="button">
        Loaded · show
      </button>
    );
  }
  if (related.location === 'other-page' && eventId !== undefined && onJumpToEvent) {
    return (
      <button className="button ghost related-event-action" onClick={() => onJumpToEvent(eventId)} type="button">
        {related.direction === 'older' ? 'Older page · jump' : 'Newer page · jump'}
      </button>
    );
  }
  if (related.location === 'pruned') {
    return (
      <span className="timeline-pruned-badge related-event-action" title={prunedRelatedEventTooltip(related)}>
        pruned
      </span>
    );
  }
  if (related.location === 'loaded') return <span className="muted related-event-action">Loaded</span>;
  return <span className="muted related-event-action">Not on this page</span>;
}
