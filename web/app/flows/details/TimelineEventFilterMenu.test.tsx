// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { useState } from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import { HorizontalTimeline } from './HorizontalTimeline';
import { timelineLabelModeIntroStorageKey } from './timelineLabelModeStorage';
import { timelineEventFilterStorageKey } from './timelineEventFilter';

function memoryStorage(entries: Record<string, string> = {}): Storage {
  const values = new Map(Object.entries(entries));
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key: string) => values.get(key) ?? null,
    key: (index: number) => [...values.keys()][index] ?? null,
    removeItem: (key: string) => { values.delete(key); },
    setItem: (key: string, value: string) => { values.set(key, String(value)); },
  };
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage({ [timelineLabelModeIntroStorageKey]: 'true' }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function step(eventId: number, type: FlowHistoryEvent['type'], stepType: string): FlowHistoryEvent {
  return {
    eventId,
    eventTime: `2026-11-17T10:00:0${eventId}Z`,
    type,
    payload: { context: { stepType, stepExecutionId: `${stepType}-${eventId}` } },
  };
}

function rpc(eventId: number, rpcName: string): FlowHistoryEvent {
  return { eventId, eventTime: `2026-11-17T10:00:0${eventId}Z`, type: 'RpcExecutionCompleted', payload: { rpcName } };
}

const pageEvents: FlowHistoryEvent[] = [
  { eventId: 1, eventTime: '2026-11-17T10:00:01Z', type: 'FlowStartedOrContinued', payload: {} },
  step(2, 'StepWaitForCompleted', 'Charge'),
  step(3, 'StepExecuteCompleted', 'Charge'),
  rpc(4, 'getStatus'),
  step(5, 'StepExecuteCompleted', 'Refund'),
  rpc(6, 'cancelOrder'),
  rpc(7, 'getStatus'),
  step(8, 'StepExecuteCompleted', 'Charge'),
];

function Harness({ flowType = 'OrderFlow', initialEventId }: { flowType?: string; initialEventId?: number }) {
  const [selected, setSelected] = useState<FlowHistoryEvent | null>(
    pageEvents.find((event) => event.eventId === initialEventId) ?? null,
  );
  return <HorizontalTimeline events={pageEvents} flowType={flowType} onSelectEvent={setSelected} selectedEvent={selected} />;
}

function laneEventIds(): number[] {
  return [...document.querySelectorAll<HTMLElement>('[role="option"]')].map((option) => Number(option.dataset.eventId));
}

function filterButton(): HTMLElement {
  return screen.getByRole('button', { name: /^Filter/ });
}

function filterDialog(): HTMLElement {
  return screen.getByRole('dialog', { name: 'Filter Timeline events', hidden: true });
}

function openFilter() {
  fireEvent.click(filterButton());
  return within(filterDialog());
}

describe('Timeline event filter', () => {
  it('starts with everything shown and a quiet Filter button', () => {
    render(<Harness />);
    expect(filterButton().className).not.toContain('is-active');
    expect(filterButton().getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByText(/events hidden on this page/)).toBeNull();
    expect(laneEventIds()).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('hides a type, shows the active state and hidden count, and resets with Show all', () => {
    render(<Harness />);
    const dialog = openFilter();
    expect(filterDialog().getAttribute('data-open')).toBe('true');
    fireEvent.click(dialog.getByRole('checkbox', { name: /^RPC/ }));

    expect(laneEventIds()).toEqual([1, 2, 3, 5, 8]);
    expect(filterButton().className).toContain('is-active');
    expect(filterButton().getAttribute('aria-label')).toBe('Filter, 1 event type filtered');
    expect(filterButton().textContent).toBe('Filter1');
    expect(screen.getByText('3 of 8 events hidden on this page')).toBeTruthy();
    expect(document.querySelectorAll('.htl-bar.has-hidden').length).toBe(3);
    expect(window.localStorage.getItem(timelineEventFilterStorageKey('OrderFlow'))).not.toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Show all' }));
    expect(laneEventIds()).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
    expect(filterButton().className).not.toContain('is-active');
    expect(screen.queryByText(/events hidden on this page/)).toBeNull();
    expect(window.localStorage.getItem(timelineEventFilterStorageKey('OrderFlow'))).toBeNull();
  });

  it('keeps name lists collapsed until the chevron or count opens them', () => {
    render(<Harness />);
    const dialog = openFilter();
    const expandExecute = dialog.getByRole('button', { name: 'Show Execute Step types' });
    expect(expandExecute.getAttribute('aria-expanded')).toBe('false');
    const executeNames = document.getElementById(expandExecute.getAttribute('aria-controls')!)!;
    expect(executeNames.getAttribute('data-expanded')).toBe('false');
    expect(executeNames.hasAttribute('inert')).toBe(true);
    expect(expandExecute.textContent).toBe('3');

    fireEvent.click(expandExecute);
    expect(expandExecute.getAttribute('aria-expanded')).toBe('true');
    expect(expandExecute.getAttribute('aria-label')).toBe('Hide Execute Step types');
    expect(executeNames.getAttribute('data-expanded')).toBe('true');
    expect(executeNames.hasAttribute('inert')).toBe(false);
    // The type checkbox still toggles the whole type, not the list.
    fireEvent.click(dialog.getByRole('checkbox', { name: /^Execute/ }));
    expect(laneEventIds()).toEqual([1, 2, 4, 6, 7]);
    expect(expandExecute.getAttribute('aria-expanded')).toBe('true');
  });

  it('opens the name list of a partly selected type by itself', () => {
    vi.stubGlobal('localStorage', memoryStorage({
      [timelineLabelModeIntroStorageKey]: 'true',
      [timelineEventFilterStorageKey('OrderFlow')]: JSON.stringify({
        hiddenKinds: [],
        nameSelections: { rpc: { mode: 'except', names: ['cancelOrder'] } },
      }),
    }));
    render(<Harness />);
    const dialog = openFilter();
    expect(dialog.getByRole('button', { name: 'Hide RPC names' }).getAttribute('aria-expanded')).toBe('true');
    expect(dialog.getByRole('button', { name: 'Show Execute Step types' }).getAttribute('aria-expanded')).toBe('false');
    fireEvent.click(dialog.getByRole('button', { name: 'Hide RPC names' }));
    expect(dialog.getByRole('button', { name: 'Show RPC names' }).getAttribute('aria-expanded')).toBe('false');
  });

  it('clears all Step types of Execute and then shows only the one picked', () => {
    render(<Harness />);
    const dialog = openFilter();
    fireEvent.click(dialog.getByRole('button', { name: 'Show Execute Step types' }));
    fireEvent.click(dialog.getByRole('button', { name: 'Clear all Execute Step types' }));
    expect(laneEventIds()).toEqual([1, 2, 4, 6, 7]);
    expect(dialog.getByRole('checkbox', { name: /^Execute/ }).getAttribute('aria-checked')).toBe('false');

    const executeNames = dialog.getByRole('list', { name: 'Execute Step types' });
    fireEvent.click(within(executeNames).getByRole('checkbox', { name: /^Refund/ }));
    expect(laneEventIds()).toEqual([1, 2, 4, 5, 6, 7]);
    expect(dialog.getByRole('checkbox', { name: /^Execute/ }).getAttribute('aria-checked')).toBe('mixed');
  });

  it('clears all types and says no events match, with Show all', () => {
    render(<Harness />);
    const dialog = openFilter();
    fireEvent.click(dialog.getByRole('button', { name: 'Clear all' }));
    expect(laneEventIds()).toEqual([]);
    expect(filterButton().getAttribute('aria-label')).toBe('Filter, 9 event types filtered');
    expect(screen.getByText('No events on this page match the filter.')).toBeTruthy();

    fireEvent.click(screen.getAllByRole('button', { name: 'Show all' })[0]);
    expect(laneEventIds()).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('keeps a selected event that the filter hides in the lane, faded', () => {
    render(<Harness initialEventId={6} />);
    const dialog = openFilter();
    fireEvent.click(dialog.getByRole('checkbox', { name: /^RPC/ }));
    expect(laneEventIds()).toEqual([1, 2, 3, 5, 6, 8]);
    const selected = document.querySelector<HTMLElement>('[role="option"][data-event-id="6"]');
    expect(selected?.className).toContain('is-filtered-out');
    expect(selected?.getAttribute('aria-selected')).toBe('true');
    expect(screen.getByText('2 of 8 events hidden on this page')).toBeTruthy();
  });

  it('reads the saved filter of its Flow type and falls back when it is corrupt', () => {
    vi.stubGlobal('localStorage', memoryStorage({
      [timelineLabelModeIntroStorageKey]: 'true',
      [timelineEventFilterStorageKey('OrderFlow')]: JSON.stringify({ hiddenKinds: ['rpc'], nameSelections: {} }),
      [timelineEventFilterStorageKey('BrokenFlow')]: '{oops',
    }));
    const { rerender } = render(<Harness flowType="OrderFlow" />);
    expect(laneEventIds()).toEqual([1, 2, 3, 5, 8]);

    rerender(<Harness flowType="RefundFlow" />);
    expect(laneEventIds()).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);

    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    rerender(<Harness flowType="BrokenFlow" />);
    expect(laneEventIds()).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
    expect(filterButton().className).not.toContain('is-active');
  });

  it('closes the popover with Escape', () => {
    render(<Harness />);
    openFilter();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(filterButton().getAttribute('aria-expanded')).toBe('false');
    expect(document.querySelector('.htl-filter-popover')?.getAttribute('data-open')).toBe('false');
  });
});
