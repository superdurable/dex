// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import { Braces, History } from 'lucide-react';
import { formatDate } from '@/lib/format';
import { eventTimeTravelTarget } from '@/lib/timeTravel';
import {
  formatTimelineOffset,
  prunedRelatedEventTooltip,
  timelineRelatedEventLabel,
  type TimelineRelatedEvent,
} from '@/lib/timeline';
import { usePreferences } from '../../providers';
import type { FlowHistoryEvent } from '@/lib/types';
import { EventDetails, eventTitle, type EventPayloadView } from './EventDetails';

function canScroll(element: HTMLElement, deltaY: number): boolean {
  if (deltaY < 0) return element.scrollTop > 0;
  if (deltaY > 0) return element.scrollTop + element.clientHeight < element.scrollHeight;
  return false;
}

function isScrollContainer(element: HTMLElement): boolean {
  const overflowY = window.getComputedStyle(element).overflowY;
  return overflowY === 'auto' || overflowY === 'scroll';
}

function containSidebarWheel(event: WheelEvent) {
  const sidebar = event.currentTarget;
  if (!(sidebar instanceof HTMLElement) || sidebar.scrollHeight <= sidebar.clientHeight) return;
  // Below the Timeline the panel is part of the page, so the page's own scroll must take the wheel.
  if (!isScrollContainer(sidebar)) return;
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
  onTimeTravelFromEvent,
}: {
  selectedEvent: FlowHistoryEvent | null;
  /** Opens time travel prefilled at the selected event; shown only for events time travel can target. */
  onTimeTravelFromEvent?: (event: FlowHistoryEvent) => void;
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
  const [payloadView, setPayloadView] = useState<EventPayloadView>('details');
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
            <span className="selected-event-actions">
              <button
                aria-pressed={payloadView === 'raw'}
                className="button ghost selected-event-json"
                onClick={() => setPayloadView((view) => view === 'raw' ? 'details' : 'raw')}
                title={payloadView === 'raw' ? 'Show the details' : 'Show the raw JSON'}
                type="button"
              >
                <Braces aria-hidden="true" size={13} strokeWidth={2.25} />
                JSON
              </button>
              {onTimeTravelFromEvent && eventTimeTravelTarget(selectedEvent) && (
                <button
                  className="button ghost selected-event-time-travel"
                  onClick={() => onTimeTravelFromEvent(selectedEvent)}
                  type="button"
                >
                  <History aria-hidden="true" size={13} strokeWidth={2.25} />
                  Time travel from here…
                </button>
              )}
            </span>
            <div className="event-meta">
              <span className="mono">#{selectedEvent.eventId}</span>
              <span title={selectedEvent.eventTime ?? undefined}>
                {selectedEvent.eventTime ? formatDate(selectedEvent.eventTime, timezone) : 'No timestamp'}
              </span>
              {offset && <span>{offset}</span>}
            </div>
            {relatedEvents.length > 0 && (
              <RelatedEvents
                relatedEvents={relatedEvents}
                isJumpDisabled={isJumpDisabled}
                onJumpToEvent={onJumpToEvent}
                onSelectRelatedEvent={onSelectRelatedEvent}
              />
            )}
          </header>
          <section className="sidebar-section selected-event-body">
            <EventDetails
              event={selectedEvent}
              history={history}
              parentFlowId={parentFlowId}
              payloadView={payloadView}
            />
          </section>
        </>
      ) : (
        <section className="sidebar-section">
          <h3>No event selected</h3>
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

/** The selected event's relations as one line under its title; each target is its own link. */
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
    <ul aria-label="Related events" className="related-events-line">
      {relatedEvents.map((related) => (
        <li className={`related-event-inline location-${related.location}`} key={related.id}>
          <span className="related-event-relation">{relationTitles[related.relation]}</span>
          <RelatedEventAction
            related={related}
            isJumpDisabled={isJumpDisabled}
            onJumpToEvent={onJumpToEvent}
            onSelectRelatedEvent={onSelectRelatedEvent}
          />
        </li>
      ))}
    </ul>
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
  const label = timelineRelatedEventLabel(related);
  if (related.location === 'loaded' && eventId !== undefined && onSelectRelatedEvent) {
    return (
      <button className="related-event-link" onClick={() => onSelectRelatedEvent(eventId)} title={`Show #${eventId}`} type="button">
        {label}
      </button>
    );
  }
  if (related.location === 'other-page' && eventId !== undefined && onJumpToEvent) {
    // The label already names the direction of the event's page.
    return (
      <button
        className="related-event-link"
        disabled={isJumpDisabled}
        onClick={() => onJumpToEvent(eventId)}
        title={`Go to #${eventId}`}
        type="button"
      >
        {label}
      </button>
    );
  }
  return (
    <span
      className="related-event-label related-event-unavailable"
      title={related.location === 'pruned' ? prunedRelatedEventTooltip(related) : undefined}
    >
      {label}
    </span>
  );
}
