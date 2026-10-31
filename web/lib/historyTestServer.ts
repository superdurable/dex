// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Test support: an in-memory history reader with server-zd paging semantics.

import type { HistoryPageLoader, HistoryPageRequest } from './history';
import type { FlowHistoryEvent, HistoryPage } from './types';

export interface HistoryTestServer {
  /** The lineage in ascending event ID order. Tests may push newer events. */
  events: FlowHistoryEvent[];
  requests: HistoryPageRequest[];
  loadPage: HistoryPageLoader;
  /** Extra response fields, such as a retained-history boundary. */
  responseFields: Record<string, unknown>;
  latestEventId: () => number;
}

export function historyTestServer(events: FlowHistoryEvent[]): HistoryTestServer {
  const server: HistoryTestServer = {
    events,
    requests: [],
    responseFields: {},
    latestEventId: () => server.events.at(-1)?.eventId ?? 0,
    loadPage: async (request) => {
      server.requests.push(request);
      return readHistoryPage(server, request);
    },
  };
  return server;
}

/** Answers a Dex Web history request URL the way `/api/flows/history` does. */
export function historyTestServerResponse(server: HistoryTestServer, url: URL): HistoryPage {
  const request: HistoryPageRequest = {
    startInternalEventId: Number(url.searchParams.get('startInternalEventId') ?? '0'),
    estimatePageSize: Number(url.searchParams.get('estimatePageSize') ?? '100'),
    nextPageToken: url.searchParams.get('nextPageToken') ?? '',
  };
  server.requests.push(request);
  return readHistoryPage(server, request);
}

/** Builds FlowStart plus Step executions whose WaitFor and Execute events follow each other. */
export function sequentialStepHistory(eventCount: number, startTime = Date.parse('2026-10-24T17:00:00Z')): FlowHistoryEvent[] {
  const events: FlowHistoryEvent[] = [{
    eventId: 1,
    eventTime: new Date(startTime).toISOString(),
    type: 'FlowStartedOrContinued',
    payload: { initialStart: { startStepType: 'Step' } },
    runAttempt: 1,
  }];
  let previousExecutionId = '__start__';
  for (let eventId = 2; eventId <= eventCount; eventId += 2) {
    const stepExecutionId = `Step-${eventId / 2}`;
    const context = { stepExecutionId, fromStepExecutionId: previousExecutionId, stepType: 'Step' };
    events.push(stepEvent(eventId, 'StepWaitForCompleted', context, startTime));
    if (eventId + 1 <= eventCount) {
      events.push({
        ...stepEvent(eventId + 1, 'StepExecuteCompleted', context, startTime),
        payload: {
          context,
          input: { conditionResults: {} },
          output: { stepDecision: { nextSteps: [{ stepType: 'Step' }] } },
        },
      });
    }
    previousExecutionId = stepExecutionId;
  }
  return events;
}

export function stepEvent(
  eventId: number,
  type: FlowHistoryEvent['type'],
  context: Record<string, unknown>,
  startTime = Date.parse('2026-10-24T17:00:00Z'),
): FlowHistoryEvent {
  return {
    eventId,
    eventTime: new Date(startTime + eventId * 1000).toISOString(),
    type,
    payload: { context },
    runAttempt: 1,
  };
}

function readHistoryPage(server: HistoryTestServer, request: HistoryPageRequest): HistoryPage {
  let firstEventId = Math.max(1, request.startInternalEventId);
  if (request.nextPageToken) firstEventId = Math.max(firstEventId, Number(request.nextPageToken) + 1);
  const size = request.estimatePageSize > 0 ? request.estimatePageSize : 100;
  const start = firstIndexAtOrAfter(server.events, firstEventId);
  const events = server.events.slice(start, start + size);
  const hasMore = start + size < server.events.length;
  return {
    ...server.responseFields,
    events,
    nextPageToken: hasMore ? String(events[events.length - 1].eventId) : '',
    nextInternalEventId: events.length > 0 ? events[events.length - 1].eventId + 1 : firstEventId,
  } as HistoryPage;
}

function firstIndexAtOrAfter(events: FlowHistoryEvent[], eventId: number): number {
  let low = 0;
  let high = events.length;
  while (low < high) {
    const middle = (low + high) >> 1;
    if (events[middle].eventId < eventId) low = middle + 1;
    else high = middle;
  }
  return low;
}
