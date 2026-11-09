// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { useState } from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { TimelineSelectedRelations } from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import { HorizontalTimeline } from './HorizontalTimeline';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function events(count: number): FlowHistoryEvent[] {
  return Array.from({ length: count }, (_, index) => ({
    eventId: index + 1,
    eventTime: '2026-10-24T17:00:00Z',
    type: 'StepExecuteCompleted',
    payload: { context: { stepType: 'Charge', stepExecutionId: `Charge-${index + 1}` } },
  }));
}

/** Holds the selection like the Run page does. */
function Harness({
  pageEvents,
  initialEventId,
  relations,
  onJumpToEvent,
}: {
  pageEvents: FlowHistoryEvent[];
  initialEventId?: number;
  relations?: TimelineSelectedRelations;
  onJumpToEvent?: (eventId: number) => void;
}) {
  const [selected, setSelected] = useState<FlowHistoryEvent | null>(
    pageEvents.find((event) => event.eventId === initialEventId) ?? null,
  );
  return (
    <HorizontalTimeline
      events={pageEvents}
      selectedEvent={selected}
      onSelectEvent={setSelected}
      relations={relations}
      onJumpToEvent={onJumpToEvent}
    />
  );
}

function option(eventId: number): HTMLElement | null {
  return document.querySelector<HTMLElement>(`[role="option"][data-event-id="${eventId}"]`);
}

describe('horizontal Timeline', () => {
  it('opens on the newest events and renders only the events in view', () => {
    render(<Harness pageEvents={events(1000)} />);
    expect(option(1000)).not.toBeNull();
    expect(option(1)).toBeNull();
    expect(document.querySelectorAll('[role="option"]').length).toBeLessThan(60);
    expect(option(1000)?.getAttribute('aria-label')).toBe('#1000 Execute completed, Charge · Charge-1000');
  });

  it('selects an event by click and moves the selection with the keyboard', () => {
    render(<Harness pageEvents={events(100)} />);
    fireEvent.click(option(100)!);
    expect(option(100)?.getAttribute('aria-selected')).toBe('true');

    const strip = screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' });
    fireEvent.keyDown(strip, { key: 'ArrowLeft' });
    expect(option(99)?.getAttribute('aria-selected')).toBe('true');
    expect(strip.getAttribute('aria-activedescendant')).toBe('htl-event-99');

    fireEvent.keyDown(strip, { key: 'PageUp' });
    expect(option(74)?.getAttribute('aria-selected')).toBe('true');

    fireEvent.keyDown(strip, { key: 'Home' });
    expect(option(1)?.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(strip, { key: 'End' });
    expect(option(100)?.getAttribute('aria-selected')).toBe('true');
  });

  it('moves the view from the overview by keyboard and by pointer', () => {
    render(<Harness pageEvents={events(1000)} />);
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    expect(Number(overview.getAttribute('aria-valuenow'))).toBe(975);

    fireEvent.keyDown(overview, { key: 'Home' });
    expect(overview.getAttribute('aria-valuenow')).toBe('0');
    expect(option(1)).not.toBeNull();
    expect(option(1000)).toBeNull();

    // jsdom has no PointerEvent, so give pointer events the mouse event fields they carry.
    vi.stubGlobal('PointerEvent', class extends MouseEvent {
      pointerId = 1;
    });
    overview.getBoundingClientRect = () => ({ left: 0, width: 1000, top: 0, height: 34, right: 1000, bottom: 34, x: 0, y: 0, toJSON: () => ({}) });
    fireEvent.pointerDown(overview, { button: 0, clientX: 500, pointerId: 1 });
    expect(option(500)).not.toBeNull();
    expect(overview.getAttribute('aria-valuetext')).toBe('Events #489 to #513');
  });

  it('marks linked events and jumps to a related event on another page', () => {
    const onJumpToEvent = vi.fn();
    const relations: TimelineSelectedRelations = {
      links: [{
        kind: 'lineage',
        stepExecutionId: 'Charge-50',
        fromEventId: 40,
        toEventId: 50,
        label: 'Charge-50',
        elapsedDurationMs: null,
        lane: 0,
      }],
      stubs: [{
        id: 'stub-older',
        kind: 'lineage',
        relation: 'source',
        direction: 'older',
        stepExecutionId: 'Charge-50',
        description: 'Review-1 WaitFor completed',
        eventId: 2,
        location: 'other-page',
        elapsedDurationMs: null,
      }],
      related: [],
    };
    render(<Harness pageEvents={events(60).slice(10)} initialEventId={50} relations={relations} onJumpToEvent={onJumpToEvent} />);
    expect(option(40)?.className).toContain('linked');
    expect(document.querySelectorAll('.htl-arc')).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: '← from Review-1 WaitFor completed, event #2 (older page)' }));
    expect(onJumpToEvent).toHaveBeenCalledWith(2);
  });

  it('brings a jumped-to event into view and highlights it', () => {
    const pageEvents = events(1000);
    const { rerender } = render(
      <HorizontalTimeline events={pageEvents} selectedEvent={null} onSelectEvent={() => {}} />,
    );
    expect(option(20)).toBeNull();
    rerender(
      <HorizontalTimeline
        events={pageEvents}
        selectedEvent={pageEvents[19]}
        onSelectEvent={() => {}}
        highlightedEventId={20}
      />,
    );
    expect(option(20)?.className).toContain('jump-highlighted');
    expect(option(20)?.getAttribute('aria-selected')).toBe('true');
  });

  it('labels events briefly and numbers every tenth event on the axis', () => {
    render(<Harness pageEvents={events(100)} initialEventId={97} />);
    expect(option(97)?.querySelector('.htl-label')?.textContent).toBe('Execute · Charge');
    expect(option(97)?.getAttribute('title')).toBeNull();
    const axis = [...document.querySelectorAll('.htl-axis-label')].map((label) => label.textContent);
    expect(axis).toContain('#90');
    expect(axis).toContain('#100');
    expect(axis).toContain('#97');
    expect(axis).not.toContain('#96');
  });

  it('keeps a selected older event in view while Live adds newer events', () => {
    const { rerender } = render(
      <HorizontalTimeline events={events(100)} selectedEvent={events(100)[94]} onSelectEvent={() => {}} />,
    );
    expect(option(95)).not.toBeNull();
    rerender(<HorizontalTimeline events={events(160)} selectedEvent={events(160)[94]} onSelectEvent={() => {}} />);
    expect(option(95)).not.toBeNull();
    expect(option(160)).toBeNull();
  });
});

