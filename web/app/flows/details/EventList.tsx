// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useRef, type KeyboardEvent } from 'react';
import { History } from 'lucide-react';
import { formatTimelineOffset } from '@/lib/timeline';
import { eventTimeTravelTarget } from '@/lib/timeTravel';
import type { FlowHistoryEvent } from '@/lib/types';
import { eventTitle } from './EventDetails';
import { eventSubject } from './HorizontalTimeline';

/** The tone of an event's dot: failures, waits and closes stand out from ordinary progress. */
export function eventListTone(event: FlowHistoryEvent): 'failed' | 'waiting' | 'closed' | 'normal' {
  if (event.type.endsWith('Failed')) return 'failed';
  if (event.type.startsWith('StepWaitFor')) return 'waiting';
  if (event.type === 'FlowClosed') return 'closed';
  return 'normal';
}

function executionIdOf(event: FlowHistoryEvent): string {
  const context = event.payload.context;
  if (!context || typeof context !== 'object') return '';
  const id = (context as Record<string, unknown>).stepExecutionId;
  return typeof id === 'string' ? id : '';
}

/** One page of events as one-line rows, newest first, for scanning beside the selected event. */
export function EventList({
  events,
  selectedEvent,
  onSelectEvent,
  onTimeTravelFromEvent,
  runStartTime,
}: {
  events: readonly FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
  onTimeTravelFromEvent?: (event: FlowHistoryEvent) => void;
  runStartTime?: string | null;
}) {
  const rows = useMemo(() => [...events].sort((left, right) => right.eventId - left.eventId), [events]);
  const listRef = useRef<HTMLDivElement>(null);
  const startMs = runStartTime ? Date.parse(runStartTime) : Number.NaN;
  const selectedIndex = selectedEvent ? rows.findIndex((event) => event.eventId === selectedEvent.eventId) : -1;

  useEffect(() => {
    if (!selectedEvent) return;
    listRef.current
      ?.querySelector<HTMLElement>(`[data-event-id="${selectedEvent.eventId}"]`)
      ?.scrollIntoView?.({ block: 'nearest' });
  }, [selectedEvent]);

  const moveWithKeys = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = { ArrowDown: 1, j: 1, ArrowUp: -1, k: -1 }[event.key];
    let next = -1;
    if (step !== undefined) next = Math.min(rows.length - 1, Math.max(0, (selectedIndex < 0 ? -1 : selectedIndex) + step));
    else if (event.key === 'Home') next = 0;
    else if (event.key === 'End') next = rows.length - 1;
    else if (event.key === 't' && selectedEvent && onTimeTravelFromEvent && eventTimeTravelTarget(selectedEvent)) {
      event.preventDefault();
      onTimeTravelFromEvent(selectedEvent);
      return;
    }
    if (next < 0 || !rows[next]) return;
    event.preventDefault();
    onSelectEvent(rows[next]);
  };

  return (
    <div
      aria-activedescendant={selectedEvent ? `event-row-${selectedEvent.eventId}` : undefined}
      aria-label="Events, newest first. Arrow keys or J and K move; T time travels from the selected event."
      className="event-list"
      onKeyDown={moveWithKeys}
      ref={listRef}
      role="listbox"
      tabIndex={0}
    >
      {rows.map((event) => {
        const eventMs = event.eventTime ? Date.parse(event.eventTime) : Number.NaN;
        const offset = Number.isFinite(eventMs) && Number.isFinite(startMs)
          ? `+${formatTimelineOffset((eventMs - startMs) / 1000)}`
          : '';
        const title = eventTitle(event);
        const subject = eventSubject(event);
        const executionId = executionIdOf(event);
        const isSelected = selectedEvent?.eventId === event.eventId;
        return (
          <div
            aria-selected={isSelected}
            className={`event-list-row tone-${eventListTone(event)}${isSelected ? ' is-selected' : ''}`}
            data-event-id={event.eventId}
            id={`event-row-${event.eventId}`}
            key={event.eventId}
            onClick={() => onSelectEvent(event)}
            role="option"
          >
            <span aria-hidden="true" className="event-list-dot" />
            <span className="event-list-id mono">#{event.eventId}</span>
            <span className="event-list-title">{title}</span>
            <span className="event-list-subject mono">
              {subject !== title ? subject : ''}
              {executionId && executionId !== subject ? <small> · {executionId}</small> : null}
            </span>
            <span className="event-list-offset">{offset}</span>
            {onTimeTravelFromEvent && eventTimeTravelTarget(event) && (
              <button
                aria-label={`Time travel from event #${event.eventId}`}
                className="event-list-action"
                onClick={(clickEvent) => {
                  clickEvent.stopPropagation();
                  onTimeTravelFromEvent(event);
                }}
                tabIndex={-1}
                title="Time travel from here"
                type="button"
              >
                <History aria-hidden="true" size={13} strokeWidth={2.25} />
              </button>
            )}
          </div>
        );
      })}
    </div>
  );
}
