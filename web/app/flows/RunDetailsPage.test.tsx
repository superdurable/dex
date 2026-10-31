// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, configure, fireEvent, getConfig, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import {
  historyTestServer,
  historyTestServerResponse,
  sequentialStepHistory,
  stepEvent,
  type HistoryTestServer,
} from '@/lib/historyTestServer';
import type { FlowHistoryEvent, FlowState, FlowSummary, HistoryPage } from '@/lib/types';
import { RunDetailsPage } from './RunDetailsPage';

const emptyState: FlowState = {
  flowConfig: {},
  attributes: [],
  activeStepExecutions: [],
  queuedSteps: [],
  pendingChannelMessages: {},
  completedSteps: [],
};

function summary(runAttempt: number): FlowSummary {
  return {
    flowId: 'order-1',
    runId: 'run-1',
    firstRunId: 'run-1',
    requestId: 'request-1',
    flowType: 'OrderFlow',
    flowStatus: 'Running',
    flowStatusCode: 1,
    startTime: '2026-10-24T17:00:00Z',
    closeTime: null,
    runAttempt,
  };
}

function historyEvent(eventId: number, runAttempt: number, type: FlowHistoryEvent['type']): FlowHistoryEvent {
  return { eventId, eventTime: '2026-10-24T17:00:00Z', type, payload: {}, runAttempt };
}

const firstAttemptLineage = [
  historyEvent(1, 1, 'FlowStartedOrContinued'),
  historyEvent(2, 1, 'StepWaitForCompleted'),
  historyEvent(3, 1, 'ChannelExternalDelete'),
];
const secondAttemptLineage = [
  historyEvent(1, 1, 'FlowStartedOrContinued'),
  historyEvent(4, 2, 'TimeTravelFork'),
];

interface FakeDex {
  fetch: ReturnType<typeof vi.fn>;
  historyRequests: URLSearchParams[];
  /** Live long polls; the newest-event probe waits from event 1 and is not counted. */
  waitRequests: URLSearchParams[];
  timeTravel: () => void;
}

/** Serves a lineage with server-zd paging: events at or after the cursor, at most one page. */
function lineagePage(lineage: FlowHistoryEvent[], params: URLSearchParams): HistoryPage {
  const start = Math.max(1, Number(params.get('startInternalEventId')));
  const size = Number(params.get('estimatePageSize') || '100');
  const matching = lineage.filter((event) => event.eventId >= start);
  const events = matching.slice(0, size);
  return {
    events,
    nextPageToken: matching.length > size ? String(events[events.length - 1].eventId) : '',
    nextInternalEventId: events.length > 0 ? events[events.length - 1].eventId + 1 : start,
  };
}

function isNewestEventProbe(url: URL): boolean {
  return url.pathname === '/api/flows/wait' && url.searchParams.get('nextInternalEventId') === '1';
}

function fakeDex(): FakeDex {
  let currentRunAttempt = 1;
  let releaseWait: (() => void) | null = null;
  const historyRequests: URLSearchParams[] = [];
  const waitRequests: URLSearchParams[] = [];
  const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://dex.test');
    if (url.pathname === '/api/flows/summary') return json(summary(currentRunAttempt));
    if (url.pathname === '/api/flows/state') return json(emptyState);
    if (url.pathname === '/api/flows/history') {
      historyRequests.push(url.searchParams);
      const requestedRunAttempt = Number(url.searchParams.get('runAttempt') ?? '0');
      const lineage = requestedRunAttempt === 1 || currentRunAttempt === 1 ? firstAttemptLineage : secondAttemptLineage;
      return json(lineagePage(lineage, url.searchParams));
    }
    if (isNewestEventProbe(url)) {
      const lineage = currentRunAttempt === 1 ? firstAttemptLineage : secondAttemptLineage;
      return json({ eventAvailable: true, availableInternalEventId: String(lineage.at(-1)?.eventId) });
    }
    if (url.pathname === '/api/flows/wait') {
      waitRequests.push(url.searchParams);
      await new Promise<void>((resolve, reject) => {
        releaseWait = resolve;
        init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
      return json({ eventAvailable: true });
    }
    throw new Error(`unexpected request ${input}`);
  });
  return {
    fetch: fetchMock,
    historyRequests,
    waitRequests,
    timeTravel: () => {
      currentRunAttempt = 2;
      releaseWait?.();
    },
  };
}

function renderRun(initialEntry = '/v1/flows/order-1/run-1') {
  render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <PreferencesProvider>
        <RunDetailsPage
          breadcrumb={null}
          flowId="order-1"
          runId="run-1"
          runPath={(runID) => `/v1/flows/order-1/${runID}`}
        />
      </PreferencesProvider>
    </MemoryRouter>,
  );
}

function latestEventNumber(): string {
  const label = screen.getByText('Latest event');
  return label.parentElement?.querySelector('b')?.textContent ?? '';
}

/** Each pair is [startInternalEventId, estimatePageSize]. */
function historyReads(requests: URLSearchParams[]): Array<[string | null, string | null]> {
  return requests.map((params) => [params.get('startInternalEventId'), params.get('estimatePageSize')]);
}

function memoryStorage(entries: Record<string, string>): Storage {
  const values = new Map(Object.entries(entries));
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, value); },
  };
}

beforeEach(() => {
  // The overview tab avoids layout APIs that jsdom does not implement.
  vi.stubGlobal('localStorage', memoryStorage({ 'dex.run.selected-tab': 'overview' }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('run attempts on the run page', () => {
  it('reloads the newest history window when time travel creates a new run attempt with the same run ID', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    const attemptSelect = await screen.findByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement;
    expect(attemptSelect.value).toBe('1');
    await waitFor(() => expect(latestEventNumber()).toBe('#3'));
    await waitFor(() => expect(dex.waitRequests).toHaveLength(1));

    dex.timeTravel();

    await waitFor(() => expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2'));
    await waitFor(() => expect(latestEventNumber()).toBe('#4'));
    // Each load reads FlowStart and one newest page; none rereads the history from event 0.
    expect(historyReads(dex.historyRequests)).toEqual([['1', '2'], ['1', '1000'], ['1', '2'], ['1', '1000']]);
    expect(dex.historyRequests.every((params) => !params.has('runAttempt'))).toBe(true);
    expect(screen.getByRole('option', { name: '2 (current)' })).toBeTruthy();
    expect(screen.getByRole('option', { name: '1' })).toBeTruthy();
  });

  it('shows an earlier run attempt lineage without following live history', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    dex.timeTravel();
    renderRun();

    const attemptSelect = await screen.findByRole('combobox', { name: 'Run attempt' });
    await waitFor(() => expect(dex.waitRequests).toHaveLength(1));
    fireEvent.change(attemptSelect, { target: { value: '1' } });

    await screen.findByText(/Showing the history of run attempt 1/);
    await waitFor(() => expect(latestEventNumber()).toBe('#3'));
    expect(dex.historyRequests.at(-1)?.get('runAttempt')).toBe('1');
    expect(dex.waitRequests).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'View current attempt' }));
    await waitFor(() => expect(screen.queryByText(/Showing the history of run attempt 1/)).toBeNull());
    await waitFor(() => expect(latestEventNumber()).toBe('#4'));
    expect(dex.historyRequests.at(-1)?.has('runAttempt')).toBe(false);
  });

  it('replaces the lineage on Refresh after another client time traveled while Live was off', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await waitFor(() => expect(latestEventNumber()).toBe('#3'));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Live' }));
    dex.timeTravel();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));

    await waitFor(() => expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2'));
    await waitFor(() => expect(latestEventNumber()).toBe('#4'));
    expect(historyReads(dex.historyRequests)).toEqual([['1', '2'], ['1', '1000'], ['1', '2'], ['1', '1000']]);
  });

  it('follows the current run attempt when the requested attempt does not exist', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    dex.timeTravel();
    renderRun('/v1/flows/order-1/run-1?runAttempt=7');

    await waitFor(() => expect(latestEventNumber()).toBe('#4'));
    expect(screen.queryByText(/Showing the history of run attempt/)).toBeNull();
    expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2');
    expect(dex.historyRequests.every((params) => !params.has('runAttempt'))).toBe(true);
  });

  it('keeps the run chain and reads history from the start for Servers without run attempts', async () => {
    const dex = fakeDex();
    const historyRequests: URLSearchParams[] = [];
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    dex.fetch.mockImplementation(async (input: string) => {
      const url = new URL(input, 'http://dex.test');
      if (url.pathname === '/api/flows/summary') return new Response(JSON.stringify({ ...summary(0), runAttempt: undefined }));
      if (url.pathname === '/api/flows/state') return new Response(JSON.stringify(emptyState));
      if (url.pathname === '/api/flows/history') {
        historyRequests.push(url.searchParams);
        return new Response(JSON.stringify({ events: firstAttemptLineage.map((event) => ({ ...event, runAttempt: 0 })), nextPageToken: '', nextInternalEventId: 0 }));
      }
      // This Server cannot report the newest event, so the page falls back to reading forward.
      if (isNewestEventProbe(url)) return new Response(JSON.stringify({ error: 'wait is not supported' }), { status: 501 });
      throw new Error(`unexpected request ${input}`);
    });
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await screen.findByText('Run chain');
    expect(screen.queryByRole('combobox', { name: 'Run attempt' })).toBeNull();
    await waitFor(() => expect(latestEventNumber()).toBe('#3'));
    expect(historyReads(historyRequests)).toEqual([['1', '2'], ['1', '1000']]);
    expect(warn).toHaveBeenCalledWith(
      'Unable to locate the newest history event; reading history from the start.',
      expect.any(Error),
    );
    warn.mockRestore();
  });
});

describe('time travel on the run page', () => {
  it('navigates to the new run when the Go Server returns a new run ID', async () => {
    const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
    const timeTravelRequests: unknown[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, 'http://dex.test');
      if (url.pathname === '/api/flows/summary') return json({ ...summary(0), runAttempt: undefined });
      if (url.pathname === '/api/flows/state') return json(emptyState);
      if (url.pathname === '/api/flows/history') {
        return json({ events: firstAttemptLineage.map((event) => ({ ...event, runAttempt: 0 })), nextPageToken: '', nextInternalEventId: 4 });
      }
      if (isNewestEventProbe(url)) return json({ eventAvailable: true, availableInternalEventId: '3' });
      if (url.pathname === '/api/flows/wait') {
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
        });
      }
      if (url.pathname === '/api/flows/time-travel') {
        timeTravelRequests.push(JSON.parse(String(init?.body)));
        return json({ runId: 'run-2' });
      }
      throw new Error(`unexpected request ${input}`);
    }));
    const runPath = vi.fn((runID: string) => `/v1/flows/order-1/${runID}`);
    render(
      <MemoryRouter initialEntries={['/v1/flows/order-1/run-1']}>
        <PreferencesProvider>
          <Routes>
            <Route
              path="/v1/flows/order-1/run-1"
              element={<RunDetailsPage breadcrumb={null} flowId="order-1" runId="run-1" runPath={runPath} />}
            />
            <Route path="/v1/flows/order-1/run-2" element={<p>Run page for run-2</p>} />
          </Routes>
        </PreferencesProvider>
      </MemoryRouter>,
    );

    await screen.findByText('Run chain');
    fireEvent.click(screen.getByRole('button', { name: 'Time Travel' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Reason' }), { target: { value: 'replay' } });
    fireEvent.click(screen.getByRole('button', { name: 'Time travel' }));

    await screen.findByText('Run page for run-2');
    expect(runPath).toHaveBeenCalledWith('run-2');
    expect(timeTravelRequests).toEqual([{ flowId: 'order-1', runId: 'run-1', timeTravelType: 1, reason: 'replay' }]);
  });
});

interface PagedDex {
  fetch: ReturnType<typeof vi.fn>;
  liveWaits: URLSearchParams[];
  releaseLiveWait: () => void;
}

/** Serves a run whose history comes from a history test server, with a blocking Live wait. */
function pagedDex(server: HistoryTestServer): PagedDex {
  const liveWaits: URLSearchParams[] = [];
  let releaseLiveWait: (() => void) | null = null;
  const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://dex.test');
    if (url.pathname === '/api/flows/summary') return json(summary(1));
    if (url.pathname === '/api/flows/state') return json(emptyState);
    if (url.pathname === '/api/flows/history') return json(historyTestServerResponse(server, url));
    if (isNewestEventProbe(url)) {
      return json({ eventAvailable: true, availableInternalEventId: String(server.latestEventId()) });
    }
    if (url.pathname === '/api/flows/wait') {
      liveWaits.push(url.searchParams);
      await new Promise<void>((resolve, reject) => {
        releaseLiveWait = resolve;
        init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
      return json({ eventAvailable: true, availableInternalEventId: String(server.latestEventId()) });
    }
    throw new Error(`unexpected request ${input}`);
  });
  return { fetch: fetchMock, liveWaits, releaseLiveWait: () => releaseLiveWait?.() };
}

function timelineRowIDs(): string[] {
  return [...document.querySelectorAll<HTMLElement>('.timeline-row')].map((row) => row.dataset.eventId ?? '');
}

function timelineRow(eventId: number): HTMLElement {
  const row = document.querySelector<HTMLElement>(`.timeline-row[data-event-id="${eventId}"]`);
  if (!row) throw new Error(`event #${eventId} is not on the Timeline page`);
  return row;
}

/** FlowStart, filler events, a WaitFor, and an Execute that reports the WaitFor event ID. */
function crossPageHistory(waitForEventId: number, executeEventId: number, lastEventId: number): FlowHistoryEvent[] {
  const review = { stepExecutionId: 'Review-1', fromStepExecutionId: '__start__', stepType: 'Review' };
  return Array.from({ length: lastEventId }, (_, offset): FlowHistoryEvent => {
    const eventId = offset + 1;
    if (eventId === 1) return sequentialStepHistory(1)[0];
    if (eventId === waitForEventId) return stepEvent(eventId, 'StepWaitForCompleted', review);
    if (eventId === executeEventId) {
      return {
        ...stepEvent(eventId, 'StepExecuteCompleted', review),
        payload: { context: { ...review, waitForCompletedEventId: String(waitForEventId) }, input: { conditionResults: {} } },
      };
    }
    return { eventId, eventTime: '2026-10-24T18:00:00Z', type: 'ChannelExternalPublish', payload: { messages: [] }, runAttempt: 1 };
  });
}

// Each page renders up to 1000 rows, which takes about a second on slow CI runners.
describe('paged Timeline on the run page', { timeout: 30_000 }, () => {
  let asyncUtilTimeout = 0;
  beforeAll(() => {
    asyncUtilTimeout = getConfig().asyncUtilTimeout;
    configure({ asyncUtilTimeout: 10_000 });
  });
  afterAll(() => configure({ asyncUtilTimeout }));

  beforeEach(() => {
    vi.stubGlobal('localStorage', memoryStorage({ 'dex.run.selected-tab': 'timeline' }));
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    Element.prototype.scrollIntoView = vi.fn();
  });

  it('renders the newest page of a 20,000-event history within the time budget', { timeout: 60_000 }, async () => {
    const server = historyTestServer(sequentialStepHistory(20_000));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    const startedAt = performance.now();
    renderRun();

    await waitFor(() => expect(timelineRowIDs()).toHaveLength(1000), { timeout: 30_000 });
    const elapsedMs = performance.now() - startedAt;

    expect(elapsedMs).toBeLessThan(8000);
    expect(timelineRowIDs()[0]).toBe('20000');
    expect(timelineRowIDs().at(-1)).toBe('19001');
    expect(timelineRow(19_500).querySelector('.event-id')?.textContent).toBe('#19500');
    expect(server.requests.map((request) => [request.startInternalEventId, request.estimatePageSize]))
      .toEqual([[1, 2], [19_001, 1000]]);
  });

  it('pages to older and newer events', async () => {
    // Two full 1000-event pages and a short oldest page.
    const server = historyTestServer(sequentialStepHistory(2100));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2100'));
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1100'));
    expect(timelineRowIDs().at(-1)).toBe('101');
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('100'));
    expect(timelineRowIDs().at(-1)).toBe('1');
    expect((screen.getByRole('button', { name: 'Older events' }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Newer events' }));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1100'));
    fireEvent.click(screen.getByRole('button', { name: 'Newer events' }));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2100'));
    // The fetched page stays even at the end, so the view does not switch windows.
    expect(timelineRowIDs().at(-1)).toBe('1101');
    expect((screen.getByRole('button', { name: 'Newer events' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Newest' }));
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Newest' })).toBeNull());
  });

  it('jumps from a cross-page stub to the far end, highlights it, and goes back', async () => {
    const review = { stepExecutionId: 'Review-1', fromStepExecutionId: '__start__', stepType: 'Review' };
    const filler = Array.from({ length: 2997 }, (_, offset): FlowHistoryEvent => ({
      eventId: offset + 3,
      eventTime: '2026-10-24T18:00:00Z',
      type: 'ChannelExternalPublish',
      payload: { messages: [] },
      runAttempt: 1,
    }));
    const reviewWaitFor = stepEvent(2, 'StepWaitForCompleted', review);
    const execute: FlowHistoryEvent = {
      ...stepEvent(3000, 'StepExecuteCompleted', review),
      payload: {
        context: { ...review, waitForCompletedEventId: '2', startedTime: '2026-10-24T19:05:02Z' },
        input: { conditionResults: {} },
      },
    };
    const server = historyTestServer([sequentialStepHistory(1)[0], reviewWaitFor, ...filler, execute]);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    fireEvent.click(timelineRow(3000));
    const stub = await screen.findByRole('button', {
      name: '↓ from Review-1 WaitFor completed, event #2 (older page)',
    });
    expect(await screen.findByRole('button', { name: 'Older page · jump' })).toBeTruthy();
    fireEvent.click(stub);

    await waitFor(() => expect(timelineRow(2).className).toContain('jump-highlighted'));
    expect(timelineRow(2).className).toContain('selected');
    expect(Element.prototype.scrollIntoView).toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    expect(timelineRow(3000).className).toContain('selected');
  });

  it('shows the pruned range banner and a muted pruned stub on the oldest retained page', async () => {
    const retained = sequentialStepHistory(5000).filter((event) => event.eventId === 1 || event.eventId >= 4001);
    const server = historyTestServer(retained);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('5000'));
    expect(screen.queryByText(/removed by the history window; FlowStart is kept/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));

    await screen.findByText('Events #2 to #4000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().at(-1)).toBe('1');
    expect(timelineRowIDs().at(-2)).toBe('4001');
    fireEvent.click(timelineRow(4001));

    const stub = await screen.findByText('↓ from Step-2000 WaitFor completed (removed by the history window)');
    expect(stub.tagName).toBe('SPAN');
    expect(stub.querySelector('.timeline-pruned-badge')?.textContent).toBe('pruned');
    expect(stub.getAttribute('title')).toContain('The server keeps only the most recent window of history');
    expect(stub.getAttribute('title')).toContain('Step execution ID: Step-2000.');
  });

  it('appends Live events to the newest page without reloading the history', async () => {
    const server = historyTestServer(sequentialStepHistory(1500));
    const dex = pagedDex(server);
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1500'));
    await waitFor(() => expect(dex.liveWaits.at(-1)?.get('nextInternalEventId')).toBe('1501'));
    server.requests.length = 0;
    server.events.push(stepEvent(1501, 'StepWaitForCompleted', { stepExecutionId: 'Step-751' }));
    dex.releaseLiveWait();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1501'));
    expect(timelineRowIDs()).toHaveLength(1000);
    expect(latestEventNumber()).toBe('#1501');
    expect(server.requests).toEqual([{ startInternalEventId: 1501, estimatePageSize: 1000, nextPageToken: '' }]);
    await waitFor(() => expect(dex.liveWaits.at(-1)?.get('nextInternalEventId')).toBe('1502'));
  });

  it('refreshes a stale pruned range when a window that starts at FlowStart loads', async () => {
    const server = historyTestServer(sequentialStepHistory(6000).filter((event) => event.eventId === 1 || event.eventId >= 4001));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('6000'));
    // server-zd prunes another chunk after the run page inferred events #2 to #4000.
    server.events = server.events.filter((event) => event.eventId === 1 || event.eventId >= 5001);
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));

    await screen.findByText('Events #2 to #5000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().at(-2)).toBe('5001');
  });

  it('reclassifies a jump target that was pruned after the page loaded', async () => {
    const server = historyTestServer(crossPageHistory(4500, 6000, 6000).filter((event) => event.eventId === 1 || event.eventId >= 4001));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('6000'));
    fireEvent.click(timelineRow(6000));
    const stub = await screen.findByRole('button', { name: '↓ from Review-1 WaitFor completed, event #4500 (older page)' });
    server.events = server.events.filter((event) => event.eventId === 1 || event.eventId >= 5001);
    fireEvent.click(stub);

    const prunedStub = await screen.findByText('↓ from Review-1 WaitFor completed (event #4500, removed by the history window)');
    expect(prunedStub.tagName).toBe('SPAN');
    expect(screen.queryByText(/is not in the retained history/)).toBeNull();
    expect(timelineRowIDs()[0]).toBe('6000');
  });

  it('reloads the older page on Back after a jump from it', async () => {
    const server = historyTestServer(crossPageHistory(2, 1500, 3000));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2000'));
    fireEvent.click(timelineRow(1500));
    fireEvent.click(await screen.findByRole('button', { name: '↓ from Review-1 WaitFor completed, event #2 (older page)' }));
    await waitFor(() => expect(timelineRow(2).className).toContain('jump-highlighted'));
    server.requests.length = 0;

    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2000'));
    expect(timelineRow(1500).className).toContain('selected');
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([1001]);
  });

  it('reads the reported pruned range, retained boundary, and string context IDs', async () => {
    const events = crossPageHistory(3000, 6000, 6000)
      .filter((event) => event.eventId === 1 || event.eventId >= 4001)
      .map((event) => {
        const context = event.payload.context as Record<string, unknown> | undefined;
        return context ? { ...event, payload: { ...event.payload, context: { ...context, fromEventId: '1' } } } : event;
      });
    const server = historyTestServer(events);
    server.responseFields = { firstRetainedEventId: 4001, prunedEventIdRange: { startEventId: 2, endEventId: 4000 } };
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('6000'));
    fireEvent.click(timelineRow(6000));
    const stub = await screen.findByText('↓ from Review-1 WaitFor completed (event #3000, removed by the history window)');
    expect(stub.tagName).toBe('SPAN');
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('5000'));
    expect(timelineRowIDs().at(-1)).toBe('4001');
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));

    await screen.findByText('Events #2 to #4000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().slice(-2)).toEqual(['4001', '1']);
  });

  it('uses the reported run-wide pruned range after time travel, where the inference finds none', async () => {
    const review = { stepExecutionId: 'Review-1', fromStepExecutionId: '__start__', stepType: 'Review', fromEventId: '9001' };
    const secondAttempt: FlowHistoryEvent[] = Array.from({ length: 1000 }, (_, offset): FlowHistoryEvent => {
      const eventId = 9001 + offset;
      if (eventId === 9001) {
        return {
          eventId,
          eventTime: '2026-10-24T18:00:00Z',
          type: 'TimeTravelFork',
          payload: { previousRunAttempt: 1, targetRunAttempt: 1, targetEventId: '1' },
          runAttempt: 2,
        };
      }
      if (eventId === 10_000) {
        return {
          ...stepEvent(eventId, 'StepExecuteCompleted', review),
          payload: { context: { ...review, waitForCompletedEventId: '4000' }, input: { conditionResults: {} } },
          runAttempt: 2,
        };
      }
      return { eventId, eventTime: '2026-10-24T18:00:00Z', type: 'ChannelExternalPublish', payload: { messages: [] }, runAttempt: 2 };
    });
    const server = historyTestServer([sequentialStepHistory(1)[0], ...secondAttempt]);
    server.responseFields = { firstRetainedEventId: 5001, prunedEventIdRange: { startEventId: 2, endEventId: 5000 } };
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('10000'));
    fireEvent.click(timelineRow(10_000));
    await screen.findByText('↓ from Review-1 WaitFor completed (event #4000, removed by the history window)');
    fireEvent.click(screen.getByRole('button', { name: 'Older events' }));

    await screen.findByText('Events #2 to #5000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().slice(-2)).toEqual(['9001', '1']);
  });
});

