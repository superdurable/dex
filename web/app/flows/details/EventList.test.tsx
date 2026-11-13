// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import { EventList, eventBarsOf } from './EventList';

afterEach(cleanup);

function event(eventId: number, type: FlowHistoryEvent['type'], payload: Record<string, unknown> = {}): FlowHistoryEvent {
  return { eventId, eventTime: null, type, payload };
}

describe('event list', () => {
  it('lists newest first and moves the selection with J, K and the arrow keys', () => {
    const events = [event(1, 'FlowStartedOrContinued'), event(2, 'StepExecuteCompleted'), event(3, 'FlowClosed')];
    const onSelectEvent = vi.fn();
    render(<EventList events={events} onSelectEvent={onSelectEvent} selectedEvent={events[1]} />);
    const rows = screen.getAllByRole('option');
    expect(rows.map((row) => row.dataset.eventId)).toEqual(['3', '2', '1']);
    const list = screen.getByRole('listbox');
    fireEvent.keyDown(list, { key: 'j' });
    expect(onSelectEvent).toHaveBeenLastCalledWith(events[0]);
    fireEvent.keyDown(list, { key: 'ArrowUp' });
    expect(onSelectEvent).toHaveBeenLastCalledWith(events[2]);
    fireEvent.click(rows[0]);
    expect(onSelectEvent).toHaveBeenLastCalledWith(events[2]);
  });

  it('links a SubFlow from the first event of the Step that started it', () => {
    const events = [
      event(10, 'StepWaitForCompleted', { context: { stepType: 'CreateShipment' } }),
      event(11, 'StepExecuteCompleted', { context: { stepType: 'CreateShipment' } }),
    ];
    const child = { flowId: 'SubFlow:order-1-CreateShipment-1-0', runId: 'r', flowStatus: 'Completed' };
    render(
      <MemoryRouter>
        <EventList childFlows={[child as never]} events={events} flowId="order-1" onSelectEvent={vi.fn()} selectedEvent={null} />
      </MemoryRouter>,
    );
    const link = screen.getByRole('link', { name: /child/ });
    expect(link.closest('[data-event-id]')?.getAttribute('data-event-id')).toBe('10');
  });

  it('draws a Step from its first event on the page to this one, on the page\'s own time axis', () => {
    const at = (eventId: number, type: FlowHistoryEvent['type'], seconds: number, executionId?: string): FlowHistoryEvent => ({
      eventId, type, eventTime: new Date(Date.UTC(2026, 0, 1, 0, 0, seconds)).toISOString(),
      payload: executionId ? { context: { stepExecutionId: executionId } } : {},
    });
    const bars = eventBarsOf([
      at(1, 'FlowStartedOrContinued', 0),
      at(2, 'StepWaitForCompleted', 2, 'PickItems-1'),
      at(3, 'StepExecuteCompleted', 6, 'PickItems-1'),
      at(4, 'FlowClosed', 10),
    ]);
    expect(bars.get(1)).toEqual({ left: 0, width: 0 });
    expect(bars.get(3)).toEqual({ left: 20, width: 40 });
    expect(bars.get(4)).toEqual({ left: 100, width: 0 });
  });

  it('draws no bars for a page that spans no time', () => {
    expect(eventBarsOf([event(1, 'FlowStartedOrContinued'), event(2, 'FlowClosed')]).size).toBe(0);
  });
});
