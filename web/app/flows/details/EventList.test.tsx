// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import { EventList } from './EventList';

afterEach(cleanup);

function event(eventId: number, type: FlowHistoryEvent['type']): FlowHistoryEvent {
  return { eventId, eventTime: null, type, payload: {} };
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
});
