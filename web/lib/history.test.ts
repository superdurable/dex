// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import type { FlowHistoryEvent, HistoryPage } from './types';
import { loadCompleteHistory } from './history';

function event(eventId: number): FlowHistoryEvent {
  return { eventId, eventTime: '', type: 'FlowStartedOrContinued', payload: {} };
}

describe('loadCompleteHistory', () => {
  it('loads every page in one current run', async () => {
    const pages = new Map<string, HistoryPage>([
      ['', { events: [event(1)], nextPageToken: 'second', nextInternalEventId: 2 }],
      ['second', { events: [event(2)], nextPageToken: 'third', nextInternalEventId: 3 }],
      ['third', { events: [event(3)], nextPageToken: '', nextInternalEventId: 4 }],
    ]);
    const requests: Array<[string, number]> = [];

    const result = await loadCompleteHistory(async (pageToken, internalEventId) => {
      requests.push([pageToken, internalEventId]);
      const page = pages.get(pageToken);
      if (!page) throw new Error(`missing page ${pageToken}`);
      return page;
    });

    expect(result.events.map((entry) => entry.eventId)).toEqual([1, 2, 3]);
    expect(result.nextInternalEventId).toBe(4);
    expect(requests).toEqual([['', 0], ['second', 2], ['third', 3]]);
  });

  it('rejects a repeated page token', async () => {
    await expect(loadCompleteHistory(async () => ({
      events: [],
      nextPageToken: 'repeated',
      nextInternalEventId: 1,
    }))).rejects.toThrow('repeated page token');
  });
});
