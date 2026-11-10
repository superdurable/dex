// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  historyTestServer,
  historyTestServerResponse,
  sequentialStepHistory,
  stepEvent,
} from '@/lib/historyTestServer';
import { loadRecentRunHistory, recentRunEvents } from './recentRunHistory';

describe('loadRecentRunHistory', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('reads FlowStart and the newest window, then appends only new events on the next poll', async () => {
    const server = historyTestServer(sequentialStepHistory(20_000));
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

    const first = await loadRecentRunHistory('flow-1', 'run-1', 1);
    expect(recentRunEvents(first).map((entry) => entry.eventId).slice(0, 2)).toEqual([1, 19_001]);
    expect(recentRunEvents(first)).toHaveLength(1001);
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([1, 19_001]);

    server.requests.length = 0;
    server.events.push(stepEvent(20_001, 'StepExecuteCompleted', { stepExecutionId: 'Step-10000' }));
    const next = await loadRecentRunHistory('flow-1', 'run-1', 1, first);
    expect(next.historyWindow.events.at(-1)?.eventId).toBe(20_001);
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([20_001]);
  });
});
