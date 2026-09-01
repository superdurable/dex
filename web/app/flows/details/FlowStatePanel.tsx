// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useRef } from 'react';
import { displayEventNumber } from '@/lib/timeline';
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
}: {
  selectedEvent: FlowHistoryEvent | null;
  history: FlowHistoryEvent[];
  parentFlowId: string;
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
              <span>Event {displayEventNumber(history, selectedEvent)}</span>
              <span>{selectedEvent.eventTime || 'No timestamp'}</span>
            </div>
          </header>
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
