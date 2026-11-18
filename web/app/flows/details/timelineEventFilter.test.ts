// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  allTimelineEventsFilter,
  clearAllTimelineEventKinds,
  clearAllTimelineEventNames,
  compileTimelineEventFilter,
  filteredKindCount,
  isTimelineEventShown,
  parseTimelineEventFilter,
  persistTimelineEventFilter,
  setTimelineEventKindShown,
  storedTimelineEventFilter,
  timelineEventFilterOptions,
  timelineEventFilterStorageKey,
  timelinePageKindCounts,
  toggleTimelineEventName,
  type TimelineEventFilter,
} from './timelineEventFilter';
import { laneIndexToPageIndex, pageIndexToLaneIndex } from './timelineOverview';

function step(eventId: number, type: FlowHistoryEvent['type'], stepType: string): FlowHistoryEvent {
  return { eventId, eventTime: '', type, payload: { context: { stepType, stepExecutionId: `${stepType}-${eventId}` } } };
}

function rpc(eventId: number, rpcName: string): FlowHistoryEvent {
  return { eventId, eventTime: '', type: 'RpcExecutionCompleted', payload: { rpcName } };
}

const pageEvents: FlowHistoryEvent[] = [
  { eventId: 1, eventTime: '', type: 'FlowStartedOrContinued', payload: {} },
  step(2, 'StepWaitForCompleted', 'Charge'),
  step(3, 'StepExecuteCompleted', 'Charge'),
  step(4, 'StepExecuteCompleted', 'Refund'),
  rpc(5, 'getStatus'),
  rpc(6, 'cancelOrder'),
  rpc(7, 'getStatus'),
  { eventId: 8, eventTime: '', type: 'ChannelExternalPublish', payload: { messages: [{ channelName: 'orders' }, { channelName: 'audit' }] } },
];

function optionsFor(filter: TimelineEventFilter, events: readonly FlowHistoryEvent[]) {
  return timelineEventFilterOptions(compileTimelineEventFilter(filter), timelinePageKindCounts(events));
}

function shownIds(filter: TimelineEventFilter): number[] {
  return pageEvents.filter((event) => isTimelineEventShown(filter, event)).map((event) => event.eventId);
}

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

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Timeline event filter model', () => {
  it('shows every event by default', () => {
    expect(shownIds(allTimelineEventsFilter)).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
    expect(filteredKindCount(optionsFor(allTimelineEventsFilter, pageEvents))).toBe(0);
  });

  it('lists every kind in Legend order with page counts and sorted names', () => {
    const options = optionsFor(allTimelineEventsFilter, pageEvents);
    expect(options.map((option) => option.kind)).toEqual([
      'waitfor', 'execute', 'channel-publish', 'channel-delete', 'rpc', 'attributes', 'flow-start', 'flow-close', 'fork',
    ]);
    const rpcOption = options.find((option) => option.kind === 'rpc');
    expect(rpcOption?.count).toBe(3);
    expect(rpcOption?.names).toEqual([
      { name: 'cancelOrder', count: 1, isShown: true },
      { name: 'getStatus', count: 2, isShown: true },
    ]);
    expect(options.find((option) => option.kind === 'flow-close')?.count).toBe(0);
  });

  it('hides and shows a whole type', () => {
    const withoutRpc = setTimelineEventKindShown(allTimelineEventsFilter, 'rpc', false);
    expect(shownIds(withoutRpc)).toEqual([1, 2, 3, 4, 8]);
    const options = optionsFor(withoutRpc, pageEvents);
    expect(options.find((option) => option.kind === 'rpc')?.selection).toBe('none');
    expect(filteredKindCount(options)).toBe(1);
    expect(shownIds(setTimelineEventKindShown(withoutRpc, 'rpc', true))).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('hides one name inside a type and marks the type partly selected', () => {
    const filter = toggleTimelineEventName(allTimelineEventsFilter, 'execute', 'Refund');
    expect(shownIds(filter)).toEqual([1, 2, 3, 5, 6, 7, 8]);
    expect(optionsFor(filter, pageEvents).find((option) => option.kind === 'execute')?.selection).toBe('some');
    // WaitFor keeps its own Step type selection.
    expect(isTimelineEventShown(filter, step(9, 'StepWaitForCompleted', 'Refund'))).toBe(true);
    expect(toggleTimelineEventName(filter, 'execute', 'Refund')).toEqual(allTimelineEventsFilter);
  });

  it('clears all names of a type, then shows only the names picked', () => {
    let filter = clearAllTimelineEventNames(allTimelineEventsFilter, 'rpc');
    expect(shownIds(filter)).toEqual([1, 2, 3, 4, 8]);
    filter = toggleTimelineEventName(filter, 'rpc', 'cancelOrder');
    expect(shownIds(filter)).toEqual([1, 2, 3, 4, 6, 8]);
    // A name first seen later stays hidden, since the reader picked names.
    expect(isTimelineEventShown(filter, rpc(9, 'approve'))).toBe(false);
    expect(shownIds(setTimelineEventKindShown(filter, 'rpc', true))).toEqual([1, 2, 3, 4, 5, 6, 7, 8]);
  });

  it('keeps names first seen later shown after hiding one name', () => {
    const filter = toggleTimelineEventName(allTimelineEventsFilter, 'rpc', 'getStatus');
    expect(isTimelineEventShown(filter, rpc(9, 'approve'))).toBe(true);
  });

  it('shows an event with several names while any of them is selected', () => {
    const filter = toggleTimelineEventName(allTimelineEventsFilter, 'channel-publish', 'orders');
    expect(shownIds(filter)).toContain(8);
    expect(shownIds(toggleTimelineEventName(filter, 'channel-publish', 'audit'))).not.toContain(8);
  });

  it('clears all types, then shows a type when one of its names is picked', () => {
    const cleared = clearAllTimelineEventKinds();
    expect(shownIds(cleared)).toEqual([]);
    expect(filteredKindCount(optionsFor(cleared, pageEvents))).toBe(9);
    expect(shownIds(toggleTimelineEventName(cleared, 'rpc', 'getStatus'))).toEqual([5, 7]);
  });

  it('ignores saved names that the page does not have', () => {
    const filter: TimelineEventFilter = { hiddenKinds: [], nameSelections: { rpc: { mode: 'except', names: ['retired'] } } };
    const options = optionsFor(filter, pageEvents);
    expect(options.find((option) => option.kind === 'rpc')?.selection).toBe('all');
    expect(options.find((option) => option.kind === 'rpc')?.names.map((name) => name.name)).toEqual(['cancelOrder', 'getStatus']);
    expect(filteredKindCount(options)).toBe(0);
  });
});

describe('Timeline event filter storage', () => {
  it('saves and reads the filter per Flow type', () => {
    vi.stubGlobal('localStorage', memoryStorage());
    const filter = setTimelineEventKindShown(allTimelineEventsFilter, 'rpc', false);
    persistTimelineEventFilter('OrderFlow', filter);
    expect(storedTimelineEventFilter('OrderFlow')).toEqual(filter);
    expect(storedTimelineEventFilter('RefundFlow')).toEqual(allTimelineEventsFilter);
    expect(window.localStorage.getItem(timelineEventFilterStorageKey('OrderFlow'))).not.toBeNull();

    persistTimelineEventFilter('OrderFlow', allTimelineEventsFilter);
    expect(window.localStorage.getItem(timelineEventFilterStorageKey('OrderFlow'))).toBeNull();
  });

  it('falls back to every event for corrupt or invalid saved values', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    vi.stubGlobal('localStorage', memoryStorage({
      [timelineEventFilterStorageKey('Broken')]: '{not json',
      [timelineEventFilterStorageKey('Array')]: '[1, 2]',
    }));
    expect(storedTimelineEventFilter('Broken')).toEqual(allTimelineEventsFilter);
    expect(storedTimelineEventFilter('Array')).toEqual(allTimelineEventsFilter);
    expect(storedTimelineEventFilter(undefined)).toEqual(allTimelineEventsFilter);
  });

  it('drops the invalid parts of a saved filter', () => {
    expect(parseTimelineEventFilter(JSON.stringify({
      hiddenKinds: ['rpc', 'timer', 7, 'rpc'],
      nameSelections: {
        execute: { mode: 'only', names: ['Charge', 3] },
        waitfor: { mode: 'sometimes', names: [] },
        unknown: { mode: 'except', names: ['x'] },
      },
    }))).toEqual({ hiddenKinds: ['rpc'], nameSelections: { execute: { mode: 'only', names: ['Charge'] } } });
  });

  it('falls back to every event when storage throws', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const throwingStorage = memoryStorage();
    throwingStorage.getItem = () => { throw new Error('blocked'); };
    throwingStorage.setItem = () => { throw new Error('blocked'); };
    vi.stubGlobal('localStorage', throwingStorage);
    expect(storedTimelineEventFilter('OrderFlow')).toEqual(allTimelineEventsFilter);
    expect(() => persistTimelineEventFilter('OrderFlow', clearAllTimelineEventKinds())).not.toThrow();
  });
});

describe('lane and page index mapping', () => {
  // The lane shows page events 1, 2, and 5 of a 7-event page.
  const laneToPage = [1, 2, 5];

  it('maps lane slots to page positions', () => {
    expect(laneIndexToPageIndex(laneToPage, 7, 0)).toBe(1);
    expect(laneIndexToPageIndex(laneToPage, 7, 1.5)).toBe(2.5);
    expect(laneIndexToPageIndex(laneToPage, 7, 2)).toBe(5);
    expect(laneIndexToPageIndex(laneToPage, 7, 3)).toBe(7);
  });

  it('maps page positions back to lane slots, collapsing hidden events', () => {
    expect(pageIndexToLaneIndex(laneToPage, 0)).toBe(0);
    expect(pageIndexToLaneIndex(laneToPage, 2.5)).toBe(1.5);
    expect(pageIndexToLaneIndex(laneToPage, 3.5)).toBe(2);
    expect(pageIndexToLaneIndex(laneToPage, 6)).toBe(3);
  });

  it('is the identity when nothing is hidden', () => {
    const identity = [0, 1, 2, 3];
    expect(laneIndexToPageIndex(identity, 4, 2.25)).toBe(2.25);
    expect(pageIndexToLaneIndex(identity, 2.25)).toBe(2.25);
  });
});
