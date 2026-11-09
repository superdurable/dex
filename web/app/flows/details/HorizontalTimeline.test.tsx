// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { readFileSync } from 'node:fs';
import { useState } from 'react';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { TimelineSelectedRelations } from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import { HorizontalTimeline, horizontalTimelineLayout } from './HorizontalTimeline';

/** Events in view at the 1200 px jsdom fallback width; the label angle sets the slot width. */
const visibleCount = Math.floor((1200 - horizontalTimelineLayout.labelRunWidth) / horizontalTimelineLayout.slotWidth);

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
}: {
  pageEvents: FlowHistoryEvent[];
  initialEventId?: number;
  relations?: TimelineSelectedRelations;
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
    expect(option(1000)?.getAttribute('aria-label')).toBe('Execute completed, Charge, #1000');
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
    expect(option(99 - visibleCount)?.getAttribute('aria-selected')).toBe('true');

    fireEvent.keyDown(strip, { key: 'Home' });
    expect(option(1)?.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(strip, { key: 'End' });
    expect(option(100)?.getAttribute('aria-selected')).toBe('true');
  });

  it('moves the view from the overview by keyboard and by pointer', () => {
    render(<Harness pageEvents={events(1000)} />);
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    expect(Number(overview.getAttribute('aria-valuenow'))).toBe(1000 - visibleCount);

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
    const first = Math.round(500 - visibleCount / 2) + 1;
    expect(overview.getAttribute('aria-valuetext')).toBe(`Events #${first} to #${first + visibleCount - 1}`);
  });

  it('marks linked events and leaves related events on other pages to the event panel', () => {
    const relations: TimelineSelectedRelations = {
      links: [{
        kind: 'lineage',
        stepExecutionId: 'Charge-50',
        fromEventId: 46,
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
    render(<Harness pageEvents={events(60).slice(10)} initialEventId={50} relations={relations} />);
    expect(option(46)?.className).toContain('linked');
    expect(document.querySelectorAll('.htl-arc')).toHaveLength(1);

    expect(screen.queryByRole('button', { name: /Review-1 WaitFor completed/ })).toBeNull();
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

  it('labels events with their subject and numbers every tenth event on the axis', () => {
    render(<Harness pageEvents={events(100)} initialEventId={97} />);
    expect(option(97)?.querySelector('.htl-label')?.textContent).toBe('Charge');
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

  it('draws one overview tick per event on the page, so it follows the page size', () => {
    const { rerender } = render(<HorizontalTimeline events={events(100)} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(document.querySelectorAll('.htl-tick')).toHaveLength(100);
    rerender(<HorizontalTimeline events={events(1000)} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(document.querySelectorAll('.htl-tick')).toHaveLength(1000);
  });

  it('keeps neutral overview ticks to a thin line and gives the selection and failures the full track', () => {
    const pageEvents = events(50);
    pageEvents[9] = { ...pageEvents[9], type: 'StepExecuteFailed' };
    render(<HorizontalTimeline events={pageEvents} selectedEvent={pageEvents[44]} onSelectEvent={() => {}} />);
    const ticks = [...document.querySelectorAll('.htl-tick')];
    expect(ticks[0].getAttribute('height')).toBe('4');
    expect(ticks[9].getAttribute('class')).toContain('htl-status-failed');
    expect(ticks[9].getAttribute('height')).toBe('10');
    expect(ticks[44].getAttribute('class')).toContain('is-selected');
    expect(ticks[44].getAttribute('height')).toBe('10');
  });

  it('leaves the view where the reader moved it while the default selection follows Live', () => {
    const { rerender } = render(
      <HorizontalTimeline events={events(100)} selectedEvent={events(100)[99]} isFollowingLatest onSelectEvent={() => {}} />,
    );
    fireEvent.keyDown(screen.getByRole('slider', { name: 'Timeline overview' }), { key: 'Home' });
    rerender(
      <HorizontalTimeline events={events(101)} selectedEvent={events(101)[100]} isFollowingLatest onSelectEvent={() => {}} />,
    );
    expect(option(1)).not.toBeNull();
    expect(option(101)).toBeNull();
  });
  it('tints icons by kind family and colors only waiting, failed, and canceled events', () => {
    const at = (second: number) => `2026-10-24T17:00:${String(second).padStart(2, '0')}Z`;
    const step = (stepType: string, id: string) => ({ context: { stepType, stepExecutionId: id } });
    const pageEvents: FlowHistoryEvent[] = [
      { eventId: 1, eventTime: at(0), type: 'FlowStartedOrContinued', payload: {} },
      { eventId: 2, eventTime: at(1), type: 'StepWaitForCompleted', payload: step('Review', 'Review-1') },
      { eventId: 3, eventTime: at(2), type: 'ChannelExternalPublish', payload: { messages: [{ channelName: 'approvals' }] } },
      { eventId: 4, eventTime: at(3), type: 'StepExecuteCompleted', payload: step('Review', 'Review-1') },
      { eventId: 5, eventTime: at(4), type: 'StepExecuteFailed', payload: step('Charge', 'Charge-1') },
      { eventId: 6, eventTime: at(5), type: 'RpcExecutionCompleted', payload: { rpcName: 'getOrder' } },
      { eventId: 7, eventTime: at(6), type: 'StepWaitForCompleted', payload: step('Ship', 'Ship-1') },
    ];
    render(<HorizontalTimeline events={pageEvents} selectedEvent={null} onSelectEvent={() => {}} />);
    const dot = (eventId: number) => option(eventId)?.querySelector('.htl-dot');
    const label = (eventId: number) => option(eventId)?.querySelector('.htl-label')?.textContent;

    // Review-1 moved on to Execute, so its wait is normal; Ship-1 is still waiting.
    expect(dot(2)?.className).toContain('htl-family-step htl-status-normal');
    expect(dot(2)?.querySelector('svg')?.getAttribute('class')).toContain('lucide-hourglass');
    expect(option(2)?.getAttribute('aria-label')).toBe('Waiting for conditions, Review, conditions met, #2');
    expect(option(7)?.getAttribute('aria-label')).toBe('Waiting for conditions, Ship, still waiting, #7');
    expect(dot(7)?.className).toContain('htl-status-waiting');
    expect(dot(3)?.className).toContain('htl-family-input htl-status-normal');
    expect(dot(3)?.querySelector('svg')?.getAttribute('class')).toContain('lucide-send');
    expect(label(3)).toBe('approvals');
    expect(dot(1)?.className).toContain('htl-family-lifecycle');
    expect(dot(5)?.className).toContain('htl-status-failed');
    expect(dot(5)?.querySelector('.htl-dot-badge')).not.toBeNull();
    expect(option(5)?.getAttribute('aria-label')).toBe('Execute failed, Charge, #5');
    expect(label(6)).toBe('getOrder');
    expect(dot(7)?.querySelector('.htl-dot-badge')).toBeNull();
    expect(screen.getByRole('list', { name: 'Event kinds' })).not.toBeNull();
    expect(screen.getByRole('list', { name: 'Status colors' }).textContent).toBe('WaitingFailedCanceled');
  });

  it('marks a wait canceled when the Flow closes, and leaves it uncolored when newer pages exist', () => {
    const wait: FlowHistoryEvent = {
      eventId: 1,
      eventTime: '2026-10-24T17:00:00Z',
      type: 'StepWaitForCompleted',
      payload: { context: { stepType: 'Ship', stepExecutionId: 'Ship-1' } },
    };
    const closed: FlowHistoryEvent = { eventId: 2, eventTime: '2026-10-24T17:00:01Z', type: 'FlowClosed', payload: { flowStatus: 6 } };
    const { rerender } = render(<HorizontalTimeline events={[wait, closed]} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(option(1)?.querySelector('.htl-dot')?.className).toContain('htl-status-canceled');
    expect(option(2)?.querySelector('.htl-dot')?.className).toContain('htl-status-canceled');

    rerender(
      <HorizontalTimeline
        events={[wait]}
        selectedEvent={null}
        onSelectEvent={() => {}}
        paging={{ hasOlder: false, hasNewer: true, isLoading: false }}
      />,
    );
    expect(option(1)?.querySelector('.htl-dot')?.className).toContain('htl-status-normal');
  });

  it('keeps the keyboard hint out of sight as the strip description', () => {
    render(<Harness pageEvents={events(100)} />);
    const strip = screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' });
    const hint = document.getElementById(strip.getAttribute('aria-describedby') ?? '');
    expect(hint?.className).toBe('sr-only');
    expect(hint?.textContent).toContain('Page Up and Page Down move by a screen');
    expect(screen.queryByText('100 events')).toBeNull();
  });

  it('keeps wheel input over the strip from scrolling the page, even at either end', () => {
    const onPageWheel = vi.fn();
    render(<div onWheel={onPageWheel}><Harness pageEvents={events(1000)} /></div>);
    document.addEventListener('wheel', onPageWheel);
    const strip = screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' });
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    const wheel = (deltaX: number, deltaY: number) => {
      const event = new WheelEvent('wheel', { deltaX, deltaY, bubbles: true, cancelable: true });
      strip.dispatchEvent(event);
      return event;
    };

    // Newest end: scrolling further right is a no-op but still must not reach the page.
    expect(overview.getAttribute('aria-valuenow')).toBe(String(1000 - visibleCount));
    expect(wheel(0, 300).defaultPrevented).toBe(true);
    expect(overview.getAttribute('aria-valuenow')).toBe(String(1000 - visibleCount));

    fireEvent.keyDown(overview, { key: 'Home' });
    expect(wheel(0, -300).defaultPrevented).toBe(true);
    expect(wheel(-300, 0).defaultPrevented).toBe(true);
    expect(overview.getAttribute('aria-valuenow')).toBe('0');

    // Vertical wheel moves the view horizontally.
    act(() => { wheel(0, horizontalTimelineLayout.slotWidth * 5); });
    expect(overview.getAttribute('aria-valuenow')).toBe('5');
    expect(onPageWheel).not.toHaveBeenCalled();
    document.removeEventListener('wheel', onPageWheel);
  });
  it('lets only the dot and label of each event take clicks, so a long label never selects its neighbor', () => {
    const designCss = readFileSync('app/v1-design.css', 'utf8');
    const block = (selector: string) => {
      const start = designCss.indexOf(`${selector} {`);
      expect(start).toBeGreaterThanOrEqual(0);
      return designCss.slice(start, designCss.indexOf('}', start));
    };
    expect(block('.htl-event')).toContain('pointer-events: none');
    expect(block('.htl-event :is(.htl-dot, .htl-label)')).toContain('pointer-events: auto');

    render(<Harness pageEvents={events(100)} />);
    fireEvent.click(option(94)!.querySelector('.htl-label')!);
    expect(option(94)?.getAttribute('aria-selected')).toBe('true');
    expect(option(95)?.getAttribute('aria-selected')).toBe('false');
  });

  it('cancels waits that a Step or RPC decision cancels, and Pending records a Flow close interrupted', () => {
    const at = (second: number) => `2026-10-24T17:00:${String(second).padStart(2, '0')}Z`;
    const step = (stepType: string, id: string, from: string) => ({ context: { stepType, stepExecutionId: id, fromStepExecutionId: from } });
    const decide = (eventId: number, id: string, from: string, decision: Record<string, unknown>): FlowHistoryEvent => ({
      eventId,
      eventTime: at(eventId),
      type: 'StepExecuteCompleted',
      payload: { ...step('Decide', id, from), output: { stepDecision: decision } },
    });
    const pageEvents: FlowHistoryEvent[] = [
      { eventId: 1, eventTime: at(1), type: 'StepWaitForCompleted', payload: step('Ship', 'Ship-1', 'Root-1') },
      { eventId: 2, eventTime: at(2), type: 'StepWaitForCompleted', payload: step('Notify', 'Notify-1', 'Root-1') },
      { eventId: 3, eventTime: at(3), type: 'StepWaitForCompleted', payload: step('Notify', 'Notify-2', 'Other-1') },
      { eventId: 4, eventTime: at(4), type: 'StepWaitForCompleted', payload: step('Audit', 'Audit-1', 'Root-1') },
      decide(5, 'Decide-1', 'Root-1', { cancelStepTypes: ['Ship'], cancelSiblingStepTypes: ['Notify'] }),
      { eventId: 6, eventTime: at(6), type: 'RpcExecutionCompleted', payload: { rpcName: 'cancelAudit', stepDecision: { cancelStepTypes: ['Audit'] } } },
    ];
    const { rerender } = render(<HorizontalTimeline events={pageEvents} selectedEvent={null} onSelectEvent={() => {}} />);
    const dotClass = (eventId: number) => option(eventId)?.querySelector('.htl-dot')?.className;
    expect(dotClass(1)).toContain('htl-status-canceled');
    expect(option(1)?.getAttribute('aria-label')).toBe('Waiting for conditions, Ship, canceled, #1');
    expect(dotClass(2)).toContain('htl-status-canceled');
    // Notify-2 has another parent, so the sibling selector leaves it waiting.
    expect(dotClass(3)).toContain('htl-status-waiting');
    expect(dotClass(4)).toContain('htl-status-canceled');

    const closing: FlowHistoryEvent[] = [
      { eventId: 1, eventTime: at(1), type: 'StepExecutePending', payload: { ...step('Charge', 'Charge-1', 'Root-1'), phase: 2 } },
      { eventId: 2, eventTime: at(2), type: 'FlowClosed', payload: { flowStatus: 6 } },
    ];
    rerender(<HorizontalTimeline events={closing} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(dotClass(1)).toContain('htl-status-canceled');
    expect(option(1)?.getAttribute('aria-label')).toBe('Execute running, Charge, interrupted by the Flow close, #1');
    expect(option(2)?.getAttribute('aria-label')).toBe('Flow closed, canceled, #2');
  });

  it('centers a jump once and keeps the reader\'s view across later data refreshes', () => {
    const pageEvents = events(1000);
    const { rerender } = render(
      <HorizontalTimeline events={pageEvents} selectedEvent={pageEvents[49]} highlightedEventId={50} onSelectEvent={() => {}} />,
    );
    expect(option(50)).not.toBeNull();
    fireEvent.keyDown(screen.getByRole('slider', { name: 'Timeline overview' }), { key: 'End' });
    expect(option(1000)).not.toBeNull();

    const refreshed = events(1000);
    rerender(<HorizontalTimeline events={refreshed} selectedEvent={refreshed[49]} highlightedEventId={50} onSelectEvent={() => {}} />);
    expect(option(1000)).not.toBeNull();
    expect(option(50)).toBeNull();
  });

  it('points the listbox only at a rendered active option', () => {
    render(<Harness pageEvents={events(1000)} initialEventId={1000} />);
    const strip = screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' });
    expect(strip.getAttribute('aria-activedescendant')).toBe('htl-event-1000');

    fireEvent.keyDown(screen.getByRole('slider', { name: 'Timeline overview' }), { key: 'Home' });
    expect(option(1000)).toBeNull();
    expect(strip.getAttribute('aria-activedescendant')).toBeNull();
  });

  it('lists the Channel message deleted kind in the legend', () => {
    render(<Harness pageEvents={events(10)} />);
    expect(screen.getByRole('list', { name: 'Event kinds' }).textContent).toContain('Channel message deleted');
  });
  it('brings an explicit selection into view even when it is the same event the default selection followed', () => {
    function FollowingHarness() {
      const pageEvents = events(1000);
      const [selected, setSelected] = useState<FlowHistoryEvent | null>(null);
      return (
        <HorizontalTimeline
          events={pageEvents}
          selectedEvent={selected ?? pageEvents[999]}
          isFollowingLatest={selected === null}
          onSelectEvent={setSelected}
        />
      );
    }
    render(<FollowingHarness />);
    fireEvent.keyDown(screen.getByRole('slider', { name: 'Timeline overview' }), { key: 'Home' });
    expect(option(1000)).toBeNull();

    fireEvent.keyDown(screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' }), { key: 'End' });
    expect(option(1000)?.getAttribute('aria-selected')).toBe('true');
    expect(option(1)).toBeNull();
  });
});
