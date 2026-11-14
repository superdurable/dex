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
import { HorizontalTimeline, horizontalTimelineLayout, isSameCalendarDay } from './HorizontalTimeline';

/** Events in view at the 1200 px jsdom fallback width. */
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
    // The plot sits 10 px in from both ends of the 1200 px chart.
    overview.getBoundingClientRect = () => ({ left: 0, width: 1200, top: 0, height: 80, right: 1200, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
    const clientXAt = (ratio: number) => 10 + ratio * 1180;
    fireEvent.pointerDown(overview, { button: 0, clientX: clientXAt(0.5), pointerId: 1 });
    expect(option(500)).not.toBeNull();
    const first = Math.round(500 - visibleCount / 2) + 1;
    const moved = Math.round(600 - visibleCount / 2) + 1;
    expect(overview.getAttribute('aria-valuetext')).toBe(`Events #${first} to #${first + visibleCount - 1}`);
    fireEvent.pointerUp(overview, { clientX: clientXAt(0.5), pointerId: 1 });

    // Pressing inside the brush keeps where it was grabbed, so the view moves with the pointer without jumping.
    fireEvent.pointerDown(overview, { button: 0, clientX: clientXAt(0.5), pointerId: 1 });
    expect(overview.getAttribute('aria-valuetext')).toBe(`Events #${first} to #${first + visibleCount - 1}`);
    fireEvent.pointerMove(overview, { clientX: clientXAt(0.6), pointerId: 1 });
    expect(overview.getAttribute('aria-valuetext')).toBe(`Events #${moved} to #${moved + visibleCount - 1}`);
    fireEvent.pointerUp(overview, { clientX: clientXAt(0.6), pointerId: 1 });
    fireEvent.pointerMove(overview, { clientX: clientXAt(0.9), pointerId: 1 });
    expect(overview.getAttribute('aria-valuetext')).toBe(`Events #${moved} to #${moved + visibleCount - 1}`);

    fireEvent.keyDown(overview, { key: 'ArrowRight' });
    expect(Number(overview.getAttribute('aria-valuenow'))).toBe(Math.round(601 - visibleCount / 2));
    fireEvent.keyDown(overview, { key: 'End' });
    expect(Number(overview.getAttribute('aria-valuenow'))).toBe(1000 - visibleCount);
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
    // A hundred Charge events in a row share one flat label; each dot names itself on hover.
    expect([...document.querySelectorAll('.htl-subject')].map((label) => label.textContent)).toEqual(['Charge ×100']);
    expect(document.querySelector('.htl-subject')?.className).toContain('selected');
    expect(option(97)?.getAttribute('title')).toBe('Charge · #97');
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

  it('draws the page as a histogram, by event time when the times spread the events', () => {
    const bars = () => [...document.querySelectorAll('.htl-bar')];
    const total = () => bars().reduce((sum, bar) => sum + Number(bar.getAttribute('data-count')), 0);
    const { rerender } = render(<HorizontalTimeline events={events(1000)} selectedEvent={null} onSelectEvent={() => {}} />);
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    // The fixtures share one timestamp, so the bars follow event order and say so.
    expect(overview.getAttribute('data-axis')).toBe('index');
    expect(total()).toBe(1000);
    expect(bars().length).toBe(Math.floor((1200 - 20) / 7));
    expect(document.querySelector('.htl-head')?.textContent).toContain('#1 · ');
    expect(document.querySelector('.htl-head')?.textContent).toContain(' · #1000');
    expect(document.querySelector('.htl-head')?.textContent).toContain('spaced by event order');
    expect(document.querySelector('.htl-overview-brush')).not.toBeNull();

    // A burst of 900 events, a quiet minute, then 100 more: the gap shows as empty buckets.
    const timed = events(1000).map((event, index) => ({
      ...event,
      eventTime: new Date(Date.parse('2026-10-24T17:00:00Z') + (index < 900 ? index * 10 : 60_000 + index * 10)).toISOString(),
    }));
    rerender(<HorizontalTimeline events={timed} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(overview.getAttribute('data-axis')).toBe('time');
    expect(total()).toBe(1000);
    expect(bars().length).toBeLessThan(Math.floor((1200 - 20) / 7) / 2);
    expect(document.querySelector('.htl-head')?.textContent).toContain('1m 10s');
    expect(document.querySelector('.htl-head')?.textContent).not.toContain('spaced by event order');
  });

  it('gives each event its own wide bar on a short page, by event order when times would crowd them', () => {
    const at = (offsetMs: number) => new Date(Date.parse('2026-10-24T17:00:00Z') + offsetMs).toISOString();
    const bars = () => [...document.querySelectorAll('.htl-bar')];
    // Seven events within 300 ms and one 9 s later would crowd seven bars at the left edge.
    const crowded = events(8).map((event, index) => ({ ...event, eventTime: at(index < 7 ? index * 50 : 9300) }));
    const { rerender } = render(<HorizontalTimeline events={crowded} selectedEvent={null} onSelectEvent={() => {}} />);
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    expect(overview.getAttribute('data-axis')).toBe('index');
    expect(bars()).toHaveLength(8);
    expect(bars().every((bar) => bar.querySelector('rect')?.getAttribute('width') === '20')).toBe(true);
    expect(document.querySelector('.htl-head')?.textContent).toContain('9.3s · spaced by event order');
    // Every event is in view, so there is no brush to move.
    expect(document.querySelector('.htl-overview-brush')).toBeNull();

    const spread = events(8).map((event, index) => ({ ...event, eventTime: at(index * 1000) }));
    rerender(<HorizontalTimeline events={spread} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(overview.getAttribute('data-axis')).toBe('time');
    expect(bars()).toHaveLength(8);
    expect(bars().every((bar) => bar.querySelector('rect')?.getAttribute('width') === '20')).toBe(true);
  });

  it('marks failed and retried Steps in red, even in a bar crowded with normal events', () => {
    const pageEvents = events(1000);
    pageEvents[9] = { ...pageEvents[9], type: 'StepExecuteFailed' };
    pageEvents[499] = {
      ...pageEvents[499],
      payload: { context: { stepType: 'Charge', stepExecutionId: 'Charge-500', finalAttempt: 2, lastFailureInfo: { attempt: 1 } } },
    };
    render(<HorizontalTimeline events={pageEvents} selectedEvent={pageEvents[899]} onSelectEvent={() => {}} />);
    const failureBars = [...document.querySelectorAll('.htl-bar.has-failure')];
    expect(failureBars).toHaveLength(2);
    for (const bar of failureBars) {
      expect(Number(bar.getAttribute('data-count'))).toBeGreaterThan(4);
      expect(Number(bar.querySelector('.htl-bar-segment.htl-status-failed')?.getAttribute('height'))).toBeGreaterThanOrEqual(3);
      expect(bar.querySelector('.htl-bar-marker.htl-status-failed')).not.toBeNull();
    }
    expect(document.querySelector('.htl-overview-summary')?.textContent).toBe('spaced by event order · 2 failures');
    expect(document.querySelector('.htl-overview-selection')).not.toBeNull();

    fireEvent.keyDown(screen.getByRole('slider', { name: 'Timeline overview' }), { key: 'Home' });
    expect(option(10)?.querySelector('.htl-dot-badge-retried')).toBeNull();
    expect(screen.getByRole('list', { name: 'Status colors' }).textContent).toContain('Retried after failure');
  });

  it('shows a retried Step in the strip and says which attempt succeeded', () => {
    const pageEvents = events(20);
    pageEvents[14] = {
      ...pageEvents[14],
      payload: { context: { stepType: 'Charge', stepExecutionId: 'Charge-15', finalAttempt: 3 } },
    };
    render(<HorizontalTimeline events={pageEvents} selectedEvent={null} onSelectEvent={() => {}} />);
    expect(option(15)?.querySelector('.htl-dot-badge-retried')).not.toBeNull();
    expect(option(15)?.getAttribute('aria-label')).toBe('Execute completed, Charge, succeeded on attempt 3, #15');
    expect(option(14)?.querySelector('.htl-dot-badge-retried')).toBeNull();
  });

  it('describes a bar on hover with its time range, event count, and failures', () => {
    const start = Date.parse('2026-10-24T17:00:00Z');
    const pageEvents = events(40).map((event, index) => ({ ...event, eventTime: new Date(start + index * 1000).toISOString() }));
    pageEvents[0] = {
      ...pageEvents[0],
      payload: { context: { stepType: 'Charge', stepExecutionId: 'Charge-1', finalAttempt: 2 } },
    };
    render(<HorizontalTimeline events={pageEvents} selectedEvent={null} onSelectEvent={() => {}} />);
    vi.stubGlobal('PointerEvent', class extends MouseEvent {
      pointerId = 1;
    });
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    overview.getBoundingClientRect = () => ({ left: 0, width: 1200, top: 0, height: 80, right: 1200, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
    fireEvent.pointerMove(overview, { clientX: 2, pointerId: 1 });
    const tooltip = document.querySelector('.htl-overview-tooltip');
    expect(tooltip?.textContent).toContain('#1 · Charge');
    expect(tooltip?.textContent).toContain('1 retried after a failed attempt');
    expect(tooltip?.querySelector('strong')?.textContent).toMatch(/\d{2}:\d{2}:\d{2}/);
    fireEvent.pointerLeave(overview);
    expect(document.querySelector('.htl-overview-tooltip')).toBeNull();
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
    const label = (eventId: number) => document.querySelector(`.htl-subject[data-first-event-id="${eventId}"]`)?.textContent;

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
    expect(screen.getByRole('list', { name: 'Status colors' }).textContent).toBe('WaitingFailedCanceledRetried after failure');
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

  it('scrolls the lane with sideways swipes and Shift+wheel, and leaves a vertical wheel to the page', () => {
    const onPageWheel = vi.fn();
    render(<div onWheel={onPageWheel}><Harness pageEvents={events(1000)} /></div>);
    document.addEventListener('wheel', onPageWheel);
    const strip = screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' });
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    const wheel = (deltaX: number, deltaY: number, shiftKey = false) => {
      const event = new WheelEvent('wheel', { deltaX, deltaY, shiftKey, bubbles: true, cancelable: true });
      strip.dispatchEvent(event);
      return event;
    };

    // The page scrolls past the lane, so a vertical wheel never gets stuck on it.
    expect(wheel(0, 300).defaultPrevented).toBe(false);
    expect(overview.getAttribute('aria-valuenow')).toBe(String(1000 - visibleCount));
    expect(onPageWheel).toHaveBeenCalled();
    onPageWheel.mockClear();

    // Newest end: scrolling further right is a no-op but still must not reach the page.
    expect(wheel(300, 0).defaultPrevented).toBe(true);
    expect(overview.getAttribute('aria-valuenow')).toBe(String(1000 - visibleCount));

    fireEvent.keyDown(overview, { key: 'Home' });
    expect(wheel(-300, 0).defaultPrevented).toBe(true);
    expect(wheel(0, -300, true).defaultPrevented).toBe(true);
    expect(overview.getAttribute('aria-valuenow')).toBe('0');

    act(() => { wheel(horizontalTimelineLayout.slotWidth * 5, 0); });
    expect(overview.getAttribute('aria-valuenow')).toBe('5');
    act(() => { wheel(0, horizontalTimelineLayout.slotWidth * 5, true); });
    expect(overview.getAttribute('aria-valuenow')).toBe('10');
    expect(onPageWheel).not.toHaveBeenCalled();
    document.removeEventListener('wheel', onPageWheel);
  });
  it('lets only the dot of each event take clicks, so the space around it never selects its neighbor', () => {
    const designCss = readFileSync('app/v1-design.css', 'utf8');
    const block = (selector: string) => {
      const start = designCss.indexOf(`${selector} {`);
      expect(start).toBeGreaterThanOrEqual(0);
      return designCss.slice(start, designCss.indexOf('}', start));
    };
    expect(block('.htl-event')).toContain('pointer-events: none');
    expect(block('.htl-event .htl-dot')).toContain('pointer-events: auto');

    render(<Harness pageEvents={events(100)} />);
    fireEvent.click(option(94)!.querySelector('.htl-dot')!);
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

  it('grabs the brush by a grip outside its edge without jumping, and shows the grab cursor there', () => {
    render(<Harness pageEvents={events(1000)} />);
    vi.stubGlobal('PointerEvent', class extends MouseEvent {
      pointerId = 1;
    });
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    // Client pixels equal chart pixels at the 1200 px fallback width.
    overview.getBoundingClientRect = () => ({ left: 0, width: 1200, top: 0, height: 80, right: 1200, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
    const brushFill = () => document.querySelector('.htl-overview-brush-fill')!;
    const brushLeft = () => Number(brushFill().getAttribute('x'));
    const brushRight = () => brushLeft() + Number(brushFill().getAttribute('width'));
    // Move the brush mid-track first, so a grip press is not clamped into the brush at either end.
    fireEvent.pointerDown(overview, { button: 0, clientX: 600, pointerId: 1 });
    fireEvent.pointerUp(overview, { clientX: 600, pointerId: 1 });
    const viewText = overview.getAttribute('aria-valuetext');
    const left = brushLeft();
    const right = brushRight();

    for (const gripX of [left - 3, right + 3]) {
      fireEvent.pointerMove(overview, { clientX: gripX, pointerId: 1 });
      expect(overview.classList.contains('is-over-brush')).toBe(true);
      fireEvent.pointerDown(overview, { button: 0, clientX: gripX, pointerId: 1 });
      expect(overview.getAttribute('aria-valuetext')).toBe(viewText);
      fireEvent.pointerMove(overview, { clientX: gripX, pointerId: 1 });
      expect(brushLeft()).toBeCloseTo(left);
      fireEvent.pointerMove(overview, { clientX: gripX + 40, pointerId: 1 });
      expect(brushLeft()).toBeCloseTo(left + 40);
      fireEvent.pointerMove(overview, { clientX: gripX, pointerId: 1 });
      fireEvent.pointerUp(overview, { clientX: gripX, pointerId: 1 });
    }
    fireEvent.pointerMove(overview, { clientX: left - 20, pointerId: 1 });
    expect(overview.classList.contains('is-over-brush')).toBe(false);
  });

  it('never moves a widened brush on press, even when pressed outside the real window', () => {
    render(<Harness pageEvents={events(2000)} />);
    vi.stubGlobal('PointerEvent', class extends MouseEvent {
      pointerId = 1;
    });
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    overview.getBoundingClientRect = () => ({ left: 0, width: 1200, top: 0, height: 80, right: 1200, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
    const brushFill = () => document.querySelector('.htl-overview-brush-fill')!;
    const brushLeft = () => Number(brushFill().getAttribute('x'));
    fireEvent.pointerDown(overview, { button: 0, clientX: 600, pointerId: 1 });
    fireEvent.pointerUp(overview, { clientX: 600, pointerId: 1 });
    // The real window spans about 12 px of the 1180 px plot, so the brush is widened to 24 px.
    expect(Number(brushFill().getAttribute('width'))).toBe(24);
    const left = brushLeft();
    const viewText = overview.getAttribute('aria-valuetext');

    const pressX = left + 1;
    fireEvent.pointerDown(overview, { button: 0, clientX: pressX, pointerId: 1 });
    fireEvent.pointerMove(overview, { clientX: pressX, pointerId: 1 });
    expect(overview.getAttribute('aria-valuetext')).toBe(viewText);
    expect(brushLeft()).toBeCloseTo(left);
    fireEvent.pointerMove(overview, { clientX: pressX + 10, pointerId: 1 });
    expect(brushLeft()).toBeCloseTo(left + 10);
    fireEvent.pointerUp(overview, { clientX: pressX + 10, pointerId: 1 });
  });

  it('marks a waiting Step that had a failed attempt as a failure without hiding its status', () => {
    const pageEvents = events(1000);
    pageEvents[500] = {
      ...pageEvents[500],
      type: 'StepWaitForCompleted',
      payload: { context: { stepType: 'Charge', stepExecutionId: 'Charge-501', finalAttempt: 2 } },
    };
    render(<HorizontalTimeline events={pageEvents} selectedEvent={pageEvents[500]} onSelectEvent={() => {}} />);
    const failureBars = [...document.querySelectorAll('.htl-bar.has-failure')];
    expect(failureBars).toHaveLength(1);
    const [bar] = failureBars;
    expect(bar.querySelector('.htl-bar-marker.htl-status-failed')).not.toBeNull();
    // The status color keeps precedence in the bar, and the normal events keep their segment.
    expect(bar.querySelector('.htl-bar-segment.htl-status-waiting')).not.toBeNull();
    expect(bar.querySelector('.htl-bar-segment.htl-status-failed')).toBeNull();
    expect(bar.querySelector('.htl-bar-segment.htl-status-normal')).not.toBeNull();
    expect(document.querySelector('.htl-overview-summary')?.textContent).toBe('spaced by event order · 1 failure');

    const dot = option(501)?.querySelector('.htl-dot');
    expect(dot?.classList.contains('htl-status-waiting')).toBe(true);
    expect(option(501)?.querySelector('.htl-dot-badge-retried')).not.toBeNull();
    expect(option(501)?.getAttribute('aria-label')).toBe('Waiting for conditions, Charge, still waiting, after a failed attempt, #501');
  });

  it('compares the axis ends by the day in the selected timezone', () => {
    const previousTimezone = process.env.TZ;
    process.env.TZ = 'America/Los_Angeles';
    try {
      // Both instants share a UTC day, but 01:00 UTC is still the previous day in Los Angeles.
      const morning = Date.parse('2026-11-09T01:00:00Z');
      const evening = Date.parse('2026-11-09T20:00:00Z');
      expect(isSameCalendarDay(morning, evening, 'UTC')).toBe(true);
      expect(isSameCalendarDay(morning, evening, 'local')).toBe(false);
      expect(isSameCalendarDay(Date.parse('2026-11-09T23:30:00Z'), Date.parse('2026-11-10T00:30:00Z'), 'UTC')).toBe(false);
      expect(isSameCalendarDay(Date.parse('2026-11-09T23:30:00Z'), Date.parse('2026-11-10T00:30:00Z'), 'local')).toBe(true);
    } finally {
      if (previousTimezone === undefined) delete process.env.TZ;
      else process.env.TZ = previousTimezone;
    }
  });

  it('gives a bar a clock range from monotonic times, even when its event has no timestamp', () => {
    const at = (offsetMs: number) => new Date(Date.parse('2026-10-24T17:00:00Z') + offsetMs).toISOString();
    // Seven events within 300 ms and one 9 s later put the short page on the event-order axis.
    const pageEvents = events(8).map((event, index) => ({ ...event, eventTime: at(index < 7 ? index * 50 : 9300) }));
    pageEvents[2] = { ...pageEvents[2], eventTime: '' };
    render(<HorizontalTimeline events={pageEvents} selectedEvent={null} onSelectEvent={() => {}} />);
    vi.stubGlobal('PointerEvent', class extends MouseEvent {
      pointerId = 1;
    });
    const overview = screen.getByRole('slider', { name: 'Timeline overview' });
    expect(overview.getAttribute('data-axis')).toBe('index');
    overview.getBoundingClientRect = () => ({ left: 0, width: 1200, top: 0, height: 80, right: 1200, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
    // Event #3 sits at the center of the third of eight equal slots.
    fireEvent.pointerMove(overview, { clientX: 10 + (2.5 / 8) * 1180, pointerId: 1 });
    const tooltip = document.querySelector('.htl-overview-tooltip');
    expect(tooltip?.textContent).toContain('#3 · Charge');
    expect(tooltip?.querySelector('strong')?.textContent).toMatch(/\d{2}:\d{2}:\d{2}/);
  });

  describe('selecting from the overview', () => {
    /** Client pixels equal chart pixels at the 1200 px fallback width; the plot is inset 10 px. */
    const clientXAt = (ratio: number) => 10 + ratio * 1180;
    const selectedOptions = () => document.querySelectorAll('[role="option"][aria-selected="true"]');

    function renderOverview(pageEvents: FlowHistoryEvent[]): HTMLElement {
      render(<Harness pageEvents={pageEvents} />);
      vi.stubGlobal('PointerEvent', class extends MouseEvent {
        pointerId = 1;
      });
      const overview = screen.getByRole('slider', { name: 'Timeline overview' });
      overview.getBoundingClientRect = () => ({ left: 0, width: 1200, top: 0, height: 80, right: 1200, bottom: 80, x: 0, y: 0, toJSON: () => ({}) });
      return overview;
    }

    function click(overview: HTMLElement, clientX: number) {
      fireEvent.pointerDown(overview, { button: 0, clientX, pointerId: 1 });
      fireEvent.pointerUp(overview, { clientX, pointerId: 1 });
    }

    it('selects the event of a single-event bar and centers it in the strip', () => {
      // A short page draws one bar per event; event #20 sits at the center of the twentieth of 100 slots.
      const overview = renderOverview(events(100));
      const barX = clientXAt(19.5 / 100);
      fireEvent.pointerMove(overview, { clientX: barX, pointerId: 1 });
      expect(document.querySelector('.htl-overview-tooltip-hint')?.textContent).toBe('Click to select #20');
      expect(overview.classList.contains('is-over-brush')).toBe(false);

      click(overview, barX);
      expect(option(20)?.getAttribute('aria-selected')).toBe('true');
      expect(Number(overview.getAttribute('aria-valuenow'))).toBe(Math.round(19 - visibleCount / 2));

      // A bar inside the brush selects too, and the view recenters on the new event.
      const insideX = clientXAt(23.5 / 100);
      fireEvent.pointerMove(overview, { clientX: insideX, pointerId: 1 });
      expect(overview.classList.contains('is-over-brush')).toBe(false);
      click(overview, insideX);
      expect(option(24)?.getAttribute('aria-selected')).toBe('true');
      expect(selectedOptions()).toHaveLength(1);
      expect(Number(overview.getAttribute('aria-valuenow'))).toBe(Math.round(23 - visibleCount / 2));
    });

    it('selects the failed event of a multi-event bar', () => {
      // 1,000 events fill 168 bars by event order; the bar holding index 500 holds indexes 500 to 505.
      const pageEvents = events(1000);
      pageEvents[503] = { ...pageEvents[503], type: 'StepExecuteFailed' };
      const overview = renderOverview(pageEvents);
      click(overview, clientXAt(500.5 / 1000));
      expect(option(504)?.getAttribute('aria-selected')).toBe('true');
      expect(selectedOptions()).toHaveLength(1);
      expect(Number(overview.getAttribute('aria-valuenow'))).toBe(Math.round(503 - visibleCount / 2));
    });

    it('moves the view without selecting when the press becomes a drag', () => {
      const overview = renderOverview(events(1000));
      fireEvent.pointerDown(overview, { button: 0, clientX: clientXAt(0.5), pointerId: 1 });
      fireEvent.pointerMove(overview, { clientX: clientXAt(0.6), pointerId: 1 });
      fireEvent.pointerUp(overview, { clientX: clientXAt(0.6), pointerId: 1 });
      expect(selectedOptions()).toHaveLength(0);
      const moved = Math.round(600 - visibleCount / 2) + 1;
      expect(overview.getAttribute('aria-valuetext')).toBe(`Events #${moved} to #${moved + visibleCount - 1}`);

      // A press inside the brush that drags past the click slop is a drag, even if it returns near the press.
      fireEvent.pointerDown(overview, { button: 0, clientX: clientXAt(0.6), pointerId: 1 });
      fireEvent.pointerMove(overview, { clientX: clientXAt(0.6) + 10, pointerId: 1 });
      fireEvent.pointerMove(overview, { clientX: clientXAt(0.6) + 1, pointerId: 1 });
      fireEvent.pointerUp(overview, { clientX: clientXAt(0.6) + 1, pointerId: 1 });
      expect(selectedOptions()).toHaveLength(0);
    });

    it('jumps without selecting when the click lands on empty plot space', () => {
      // A quiet gap in the middle of the time axis leaves bars missing there.
      const start = Date.parse('2026-10-24T17:00:00Z');
      const pageEvents = events(1000).map((event, index) => ({
        ...event,
        eventTime: new Date(start + (index < 500 ? index : index + 500) * 1000).toISOString(),
      }));
      const overview = renderOverview(pageEvents);
      expect(overview.getAttribute('data-axis')).toBe('time');
      const before = overview.getAttribute('aria-valuenow');
      fireEvent.pointerMove(overview, { clientX: clientXAt(0.55), pointerId: 1 });
      expect(document.querySelector('.htl-overview-tooltip')).toBeNull();
      click(overview, clientXAt(0.55));
      expect(selectedOptions()).toHaveLength(0);
      expect(overview.getAttribute('aria-valuenow')).not.toBe(before);
    });

    it('selects the event at the brush center with Enter', () => {
      const overview = renderOverview(events(1000));
      fireEvent.keyDown(overview, { key: 'Home' });
      fireEvent.keyDown(overview, { key: 'Enter' });
      expect(option(Math.floor(visibleCount / 2) + 1)?.getAttribute('aria-selected')).toBe('true');
    });

    it('selects the middle event with Enter when the whole page fits without a brush', () => {
      const overview = renderOverview(events(10));
      expect(document.querySelector('.htl-overview-brush-fill')).toBeNull();
      fireEvent.keyDown(overview, { key: 'Enter' });
      expect(option(6)?.getAttribute('aria-selected')).toBe('true');
      expect(selectedOptions()).toHaveLength(1);
      const description = document.getElementById(overview.getAttribute('aria-describedby') ?? '');
      expect(description?.className).toBe('sr-only');
      expect(description?.textContent).toContain('Enter selects the event at the center of the brush');
    });

    it('never selects or moves the view on a grip press over a bar released without moving', () => {
      // 100 events draw one bar each, so every grip sits over a bar.
      const overview = renderOverview(events(100));
      const brushFill = () => document.querySelector('.htl-overview-brush-fill')!;
      // Move the brush mid-track by keyboard, so neither grip is clamped at an end.
      fireEvent.keyDown(overview, { key: 'Home' });
      fireEvent.keyDown(overview, { key: 'PageDown' });
      const left = Number(brushFill().getAttribute('x'));
      const right = left + Number(brushFill().getAttribute('width'));
      const viewText = overview.getAttribute('aria-valuetext');

      for (const gripX of [left - 2, right + 2]) {
        fireEvent.pointerMove(overview, { clientX: gripX, pointerId: 1 });
        expect(overview.classList.contains('is-over-brush')).toBe(true);
        expect(document.querySelector('.htl-overview-tooltip')).not.toBeNull();
        expect(document.querySelector('.htl-overview-tooltip-hint')).toBeNull();
        fireEvent.pointerDown(overview, { button: 0, clientX: gripX, pointerId: 1 });
        fireEvent.pointerMove(overview, { clientX: gripX + 2, pointerId: 1 });
        fireEvent.pointerUp(overview, { clientX: gripX + 2, pointerId: 1 });
        expect(overview.getAttribute('aria-valuetext')).toBe(viewText);
        expect(selectedOptions()).toHaveLength(0);
      }
    });
  });

  it('says what the run does now at the lane\'s newest end, and opens to the event that says so', () => {
    const onShowEndNoteEvent = vi.fn();
    const endNote = { label: 'Now · waiting · fires in 2d 6h', detail: 'Waiting for conditions', eventId: 100, isOpen: true };
    const { rerender } = render(
      <HorizontalTimeline endNote={endNote} events={events(100)} onSelectEvent={() => {}} onShowEndNoteEvent={onShowEndNoteEvent} selectedEvent={null} />,
    );
    const note = document.querySelector<HTMLDetailsElement>('.htl-end-note--lane')!;
    expect(note.querySelector('summary')?.textContent).toBe('Now · waiting · fires in 2d 6h');
    expect(note.open).toBe(false);
    fireEvent.click(note.querySelector('button')!);
    expect(onShowEndNoteEvent).toHaveBeenCalledWith(100);

    // A page that stops before the newest event has no "now" to point at.
    rerender(
      <HorizontalTimeline endNote={endNote} events={events(100)} onSelectEvent={() => {}} paging={{ hasNewer: true, hasOlder: false, isLoading: false }} selectedEvent={null} />,
    );
    expect(document.querySelector('.htl-end-note')).toBeNull();
  });

  it('groups consecutive events about the same subject under one label', () => {
    render(<HorizontalTimeline events={[
      ...events(3),
      { eventId: 4, eventTime: '2026-10-24T17:00:00Z', type: 'StepWaitForCompleted', payload: { context: { stepType: 'AwaitNextWindow', stepExecutionId: 'Await-1' } } },
    ]} onSelectEvent={() => {}} selectedEvent={null} />);
    const labels = [...document.querySelectorAll('.htl-subject')];
    expect(labels.map((label) => label.textContent)).toEqual(['Charge ×3', 'AwaitNextWindow']);
    expect(labels[0].getAttribute('title')).toBe('Charge ×3 · #1–#3');
  });

  it('moves the selection with J and K and time travels with T from anywhere on the page', () => {
    const onTimeTravelFromEvent = vi.fn();
    render(<div><input aria-label="Notes" /><HarnessWithTimeTravel onTimeTravelFromEvent={onTimeTravelFromEvent} /></div>);
    fireEvent.keyDown(document.body, { key: 'j' });
    expect(option(9)?.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(document.body, { key: 'k' });
    expect(option(10)?.getAttribute('aria-selected')).toBe('true');
    fireEvent.keyDown(document.body, { key: 't' });
    expect(onTimeTravelFromEvent).toHaveBeenCalledWith(expect.objectContaining({ eventId: 10 }));
    // Typing in a field keeps its letters.
    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Notes' }), { key: 'j' });
    expect(option(10)?.getAttribute('aria-selected')).toBe('true');
  });
});

/** Holds the selection, starting on the newest event, and passes time travel through. */
function HarnessWithTimeTravel({ onTimeTravelFromEvent }: { onTimeTravelFromEvent: (event: FlowHistoryEvent) => void }) {
  const pageEvents = events(10);
  const [selected, setSelected] = useState<FlowHistoryEvent | null>(pageEvents[9]);
  return (
    <HorizontalTimeline
      events={pageEvents}
      onSelectEvent={setSelected}
      onTimeTravelFromEvent={onTimeTravelFromEvent}
      selectedEvent={selected}
    />
  );
}
