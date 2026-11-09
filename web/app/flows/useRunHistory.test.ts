// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { act, renderHook, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { historyTestServer, historyTestServerResponse, sequentialStepHistory } from '@/lib/historyTestServer';
import { useRunHistory } from './useRunHistory';

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubHistoryFetch(eventCount: number) {
  const server = historyTestServer(sequentialStepHistory(eventCount));
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    const url = new URL(input, 'http://dex.test');
    if (url.pathname === '/api/flows/history') {
      return new Response(JSON.stringify(historyTestServerResponse(server, url)));
    }
    if (url.pathname === '/api/flows/wait') {
      return new Response(JSON.stringify({ eventAvailable: true, availableInternalEventId: String(server.latestEventId()) }));
    }
    throw new Error(`unexpected request ${input}`);
  }));
  return server;
}

function pageBounds(events: Array<{ eventId: number }> | undefined): [number, number] | undefined {
  return events && [events[0].eventId, events[events.length - 1].eventId];
}

describe('useRunHistory', () => {
  it('restores the exact Oldest page on Back after a jump', async () => {
    stubHistoryFetch(3500);
    const { result } = renderHook(() => useRunHistory('order-1', 'run-1'));
    await act(() => result.current.loadNewest(0));
    expect(pageBounds(result.current.timelineWindow?.events)).toEqual([2501, 3500]);

    await act(() => result.current.showOldest(501));
    expect(pageBounds(result.current.timelineWindow?.events)).toEqual([1, 500]);

    await act(async () => {
      expect((await result.current.jumpToEvent(2000, null)).kind).toBe('loaded');
    });
    expect(pageBounds(result.current.timelineWindow?.events)).toEqual([1500, 2499]);

    await act(async () => {
      expect((await result.current.back()).kind).toBe('restored');
    });
    await waitFor(() => expect(pageBounds(result.current.timelineWindow?.events)).toEqual([1, 500]));
    expect(result.current.timelineWindow?.hasNewer).toBe(true);
  });
});
