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
import { EventList } from './EventList';

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
});
