// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, configure, within, fireEvent, getConfig, render, screen, waitFor } from '@testing-library/react';
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

// React Flow needs layout APIs that jsdom lacks, so the graph reports the events it receives.
vi.mock('./details/ExecutionGraph', () => ({
  ExecutionGraph: ({ events, hasEarlierEvents }: { events: FlowHistoryEvent[]; hasEarlierEvents?: boolean }) => (
    <div
      data-testid="execution-graph"
      data-event-ids={`${events[0]?.eventId}..${events.at(-1)?.eventId}`}
      data-has-earlier-events={String(hasEarlierEvents ?? false)}
    />
  ),
}));

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
         
          flowId="order-1"
          runId="run-1"
          runPath={(runID) => `/v1/flows/order-1/${runID}`}
        />
      </PreferencesProvider>
    </MemoryRouter>,
  );
}

/** Reads the latest event number from the run's details popover, opening it when closed. */
function latestEventNumber(): string {
  if (!document.querySelector('[data-fact="events"]')) {
    const button = screen.queryByRole('button', { name: /^Run details for / });
    if (button) fireEvent.click(button);
  }
  const value = document.querySelector('[data-fact="events"] dd')?.textContent ?? '';
  return value === 'None yet' ? '' : value;
}

/** Pausing and resuming Live reads the run again, which replaced the Refresh button. */
function refreshByResumingLive() {
  toggleLive();
  toggleLive();
}

/** Pauses Live when it is on, or resumes it when updates are paused. */
function toggleLive() {
  fireEvent.click(screen.getByRole('button', { name: /^Live: |^Updates paused/ }));
}

/** Opens the run's details popover, where the run facts and ID copy buttons live. */
async function openRunFacts() {
  fireEvent.click(await screen.findByRole('button', { name: /^Run details for / }));
  return screen.findByRole('dialog', { name: 'Run details' });
}

function eventsRange(): string {
  return document.querySelector('.run-events-range')?.textContent ?? '';
}

function pageButton(name: 'Back' | 'Latest page' | 'Newer page' | 'Older page' | 'Oldest page'): HTMLButtonElement {
  return screen.getByRole('button', { name }) as HTMLButtonElement;
}

/** Each entry is [Latest page, Newer page, Older page, Oldest page] disabled. */
function pageButtonsDisabled(): boolean[] {
  return (['Latest page', 'Newer page', 'Older page', 'Oldest page'] as const).map((name) => pageButton(name).disabled);
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

    // A run on its first attempt offers no attempt picker.
    await waitFor(() => expect(latestEventNumber()).toBe('#3'));
    expect(screen.queryByRole('combobox', { name: 'Run attempt' })).toBeNull();
    await waitFor(() => expect(dex.waitRequests).toHaveLength(1));

    dex.timeTravel();

    await waitFor(() => expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2'));
    await waitFor(() => expect(latestEventNumber()).toBe('#4'));
    // Each load reads FlowStart and one newest page; none rereads the history from event 0.
    expect(historyReads(dex.historyRequests)).toEqual([['1', '2'], ['1', '1000'], ['1', '2'], ['1', '1000']]);
    expect(dex.historyRequests.every((params) => !params.has('runAttempt'))).toBe(true);
    const barAttempts = within(screen.getByRole('combobox', { name: 'Run attempt' }));
    expect(barAttempts.getByRole('option', { name: 'Attempt 2 of 2' })).toBeTruthy();
    expect(barAttempts.getByRole('option', { name: 'Attempt 1 of 2' })).toBeTruthy();
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

  it('replaces the lineage on resuming Live after another client time traveled while Live was off', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await waitFor(() => expect(latestEventNumber()).toBe('#3'));
    toggleLive();
    dex.timeTravel();
    toggleLive();

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

    const facts = await openRunFacts();
    await within(facts).findByText('Run chain');
    expect(screen.queryByRole('combobox', { name: 'Run attempt' })).toBeNull();
    // A running run shows no Closed fact, one hidden page heading, and copy buttons for its IDs.
    expect(within(facts).queryByText('Closed')).toBeNull();
    expect(screen.getAllByRole('heading', { level: 1 })).toHaveLength(1);
    expect(within(facts).getByRole('button', { name: 'Copy Flow ID' })).toBeTruthy();
    // The ID copy buttons announce their result.
    const writeText = vi.fn(async () => {});
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } });
    const copyRunID = within(facts).getByRole('button', { name: 'Copy run ID' });
    fireEvent.click(copyRunID);
    await waitFor(() => expect(copyRunID.className).toContain('is-copied'));
    expect(writeText).toHaveBeenCalledWith('run-1');
    expect(copyRunID.querySelector('[aria-live="polite"]')?.textContent).toBe('Copied');
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
              element={<RunDetailsPage flowId="order-1" runId="run-1" runPath={runPath} />}
            />
            <Route path="/v1/flows/order-1/run-2" element={<p>Run page for run-2</p>} />
          </Routes>
        </PreferencesProvider>
      </MemoryRouter>,
    );

    // A running run leads with Stop, so Time travel sits under More run actions.
    fireEvent.click(await screen.findByRole('button', { name: 'More run actions' }));
    fireEvent.click(screen.getByRole('menuitem', { name: /^Time travel/ }));
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
  /** Reads the next history page at once but holds its response until the returned release runs. */
  holdNextHistoryRead: () => () => void;
}

/** Serves a run whose history comes from a history test server, with a blocking Live wait. */
function pagedDex(server: HistoryTestServer, summaryRunAttempt = 1): PagedDex {
  const liveWaits: URLSearchParams[] = [];
  let releaseLiveWait: (() => void) | null = null;
  let heldHistoryRead: Promise<void> | null = null;
  const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
  const fetchMock = vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://dex.test');
    if (url.pathname === '/api/flows/summary') return json(summary(summaryRunAttempt));
    if (url.pathname === '/api/flows/state') return json(emptyState);
    if (url.pathname === '/api/flows/history') {
      const page = historyTestServerResponse(server, url);
      const hold = heldHistoryRead;
      heldHistoryRead = null;
      if (hold) await hold;
      return json(page);
    }
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
  return {
    fetch: fetchMock,
    liveWaits,
    releaseLiveWait: () => releaseLiveWait?.(),
    holdNextHistoryRead: () => {
      let release = () => {};
      heldHistoryRead = new Promise<void>((resolve) => { release = resolve; });
      return release;
    },
  };
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
    expect(screen.getByRole('group', { name: 'Events' })).toBeTruthy();
    expect(latestEventNumber()).toBe('#2100');
    expect(eventsRange()).toBe('Showing #1101–#2100 · Page 1 of 3');
    expect(pageButtonsDisabled()).toEqual([true, true, false, false]);
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1100'));
    expect(timelineRowIDs().at(-1)).toBe('101');
    expect(eventsRange()).toBe('Showing #101–#1100 · Page 2 of 3');
    expect(pageButtonsDisabled()).toEqual([false, false, false, false]);
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('100'));
    expect(timelineRowIDs().at(-1)).toBe('1');
    expect(eventsRange()).toBe('Showing #1–#100 · Page 3 of 3');
    expect(pageButtonsDisabled()).toEqual([false, false, true, true]);

    fireEvent.click(pageButton('Newer page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1100'));
    expect(eventsRange()).toBe('Showing #101–#1100 · Page 2 of 3');
    fireEvent.click(pageButton('Newer page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2100'));
    // A page that reaches the newest event is the newest page, which Live extends.
    expect(timelineRowIDs().at(-1)).toBe('1101');
    expect(eventsRange()).toBe('Showing #1101–#2100 · Page 1 of 3');
    expect(pageButtonsDisabled()).toEqual([true, true, false, false]);
  });

  it('jumps to the oldest page with Oldest page and back with Latest page', async () => {
    const server = historyTestServer(sequentialStepHistory(3500));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3500'));
    expect(eventsRange()).toBe('Showing #2501–#3500 · Page 1 of 4');
    server.requests.length = 0;
    fireEvent.click(pageButton('Oldest page'));

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('500'));
    expect(timelineRowIDs().at(-1)).toBe('1');
    // Oldest page reads from the window start and stops where Older page would have started page 3.
    expect(server.requests.map((request) => [request.startInternalEventId, request.estimatePageSize])).toEqual([[1, 1001]]);
    expect(eventsRange()).toBe('Showing #1–#500 · Page 4 of 4');
    expect(pageButtonsDisabled()).toEqual([false, false, true, true]);

    fireEvent.click(pageButton('Newer page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1500'));
    expect(eventsRange()).toBe('Showing #501–#1500 · Page 3 of 4');
    fireEvent.click(pageButton('Latest page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3500'));
    expect(pageButtonsDisabled()).toEqual([true, true, false, false]);
  });

  it('keeps an older page while Live appends, and Latest page resumes Live', async () => {
    const server = historyTestServer(sequentialStepHistory(1500));
    const dex = pagedDex(server);
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1500'));
    await waitFor(() => expect(dex.liveWaits.at(-1)?.get('nextInternalEventId')).toBe('1501'));
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('500'));
    expect(eventsRange()).toBe('Showing #1–#500 · Page 2 of 2');

    server.events.push(stepEvent(1501, 'StepWaitForCompleted', { stepExecutionId: 'Step-751' }));
    dex.releaseLiveWait();
    await waitFor(() => expect(latestEventNumber()).toBe('#1501'));
    await waitFor(() => expect(dex.liveWaits.at(-1)?.get('nextInternalEventId')).toBe('1502'));

    expect(timelineRowIDs()[0]).toBe('500');
    expect(timelineRowIDs().at(-1)).toBe('1');
    // The newest page moved, so this page is no longer one of its numbered pages.
    expect(eventsRange()).toBe('Showing #1–#500');
    expect(pageButtonsDisabled()).toEqual([false, false, true, true]);

    fireEvent.click(pageButton('Latest page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1501'));
    expect(eventsRange()).toBe('Showing #502–#1501 · Page 1 of 2');
    server.events.push(stepEvent(1502, 'StepExecuteCompleted', { stepExecutionId: 'Step-751' }));
    dex.releaseLiveWait();
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1502'));
  });

  it('keeps a Newer page that Live passed while it loaded, so no event is skipped', async () => {
    const server = historyTestServer(sequentialStepHistory(1500));
    const dex = pagedDex(server);
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1500'));
    await waitFor(() => expect(dex.liveWaits.at(-1)?.get('nextInternalEventId')).toBe('1501'));
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('500'));

    // The Newer page read sees #501 to #1500 as the end; Live then appends #1501 before it returns.
    const releasePrevPage = dex.holdNextHistoryRead();
    fireEvent.click(pageButton('Newer page'));
    await waitFor(() => expect(server.requests.at(-1)?.startInternalEventId).toBe(501));
    server.events.push(stepEvent(1501, 'StepWaitForCompleted', { stepExecutionId: 'Step-751' }));
    dex.releaseLiveWait();
    await waitFor(() => expect(latestEventNumber()).toBe('#1501'));
    releasePrevPage();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1500'));
    expect(timelineRowIDs().at(-1)).toBe('501');
    expect(pageButtonsDisabled()).toEqual([false, false, false, false]);
  });

  it('omits the page count when the Server reports no run attempts', async () => {
    // The gap after FlowStart lies outside the newest page, and these event IDs prove nothing.
    const retained = sequentialStepHistory(5500)
      .filter((event) => event.eventId === 1 || event.eventId >= 4001)
      .map((event) => ({ ...event, runAttempt: 0 }));
    const server = historyTestServer(retained);
    vi.stubGlobal('fetch', pagedDex(server, 0).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('5500'));
    expect(eventsRange()).toBe('Showing #4501–#5500 · Page 1');
    fireEvent.click(pageButton('Older page'));

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('4500'));
    expect(timelineRowIDs().slice(-2)).toEqual(['4001', '1']);
    expect(eventsRange()).toBe('Showing #1–#4500');
    expect(pageButtonsDisabled()).toEqual([false, false, true, true]);
  });

  it('draws the Execution graph from the selected page', async () => {
    const server = historyTestServer(sequentialStepHistory(2100));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2100'));
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1100'));
    // Every Events view shares one event panel: switching keeps the same element, its width and its event.
    const runContent = document.querySelector<HTMLElement>('.run-content');
    expect(runContent?.style.getPropertyValue('--run-sidebar-width')).toBe('780px');
    const panel = screen.getByRole('region', { name: 'Selected event details' });
    const selectedTitle = panel.querySelector('[data-selected-event-target]')?.textContent;
    fireEvent.click(screen.getByRole('button', { name: 'Graph' }));
    expect(document.querySelector<HTMLElement>('.run-content')?.style.getPropertyValue('--run-sidebar-width')).toBe('780px');
    expect(screen.getByRole('region', { name: 'Selected event details' })).toBe(panel);
    expect(panel.isConnected).toBe(true);
    expect(panel.querySelector('[data-selected-event-target]')?.textContent).toBe(selectedTitle);
    expect(document.querySelector('.events-picture')?.getAttribute('data-view')).toBe('graph');

    const graph = await screen.findByTestId('execution-graph');
    // FlowStart is added for start details; the page holds events #101 to #1100.
    expect(graph.dataset.eventIds).toBe('1..1100');
    expect(graph.dataset.hasEarlierEvents).toBe('true');
    expect(screen.queryByText(/The graph shows Steps from events/)).toBeNull();
    expect(eventsRange()).toBe('Showing #101–#1100 · Page 2 of 3');

    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(screen.getByTestId('execution-graph').dataset.eventIds).toBe('1..100'));
    expect(screen.getByTestId('execution-graph').dataset.hasEarlierEvents).toBe('false');
  });

  it('shows the whole retained history on one page when it fits', async () => {
    // The manual case: FlowStart and events #1001 to #1502 are retained.
    const retained = sequentialStepHistory(1502).filter((event) => event.eventId === 1 || event.eventId >= 1001);
    const server = historyTestServer(retained);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()).toHaveLength(503));
    expect(timelineRowIDs()[0]).toBe('1502');
    expect(timelineRowIDs().slice(-2)).toEqual(['1001', '1']);
    expect(screen.getByText('Events #2 to #1000 were removed by the history window; FlowStart is kept.')).toBeTruthy();
    expect(eventsRange()).toBe('Showing #1–#1502 · Page 1 of 1');
    // One page needs no page buttons.
    expect(screen.queryByRole('group', { name: 'Event pages' })).toBeNull();
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
      name: '↓ From Review-1 · WaitFor completed · #2 (older page)',
    });
    // The panel's relation line names the event and says which way its page is.
    const jump = await screen.findByRole('button', { name: 'Review-1 · WaitFor completed · #2 (older page)' });
    expect(jump.getAttribute('title')).toBe('Go to #2');
    fireEvent.click(stub);

    await waitFor(() => expect(timelineRow(2).className).toContain('jump-highlighted'));
    expect(timelineRow(2).className).toContain('selected');
    await waitFor(() => expect(Element.prototype.scrollIntoView).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    expect(timelineRow(3000).className).toContain('selected');
  });

  it('jumps from a related event on the horizontal Timeline and goes back', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(min-width: 761px)',
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }));
    const review = { stepExecutionId: 'Review-1', fromStepExecutionId: '__start__', stepType: 'Review' };
    const filler = Array.from({ length: 2997 }, (_, offset): FlowHistoryEvent => ({
      eventId: offset + 3,
      eventTime: '2026-10-24T18:00:00Z',
      type: 'ChannelExternalPublish',
      payload: { messages: [] },
      runAttempt: 1,
    }));
    const execute: FlowHistoryEvent = {
      ...stepEvent(3000, 'StepExecuteCompleted', review),
      payload: {
        context: { ...review, waitForCompletedEventId: '2', startedTime: '2026-10-24T19:05:02Z' },
        input: { conditionResults: {} },
      },
    };
    const server = historyTestServer([
      sequentialStepHistory(1)[0],
      stepEvent(2, 'StepWaitForCompleted', review),
      ...filler,
      execute,
    ]);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    const horizontalOption = (eventId: number) => document.querySelector<HTMLElement>(
      `[role="option"][data-event-id="${eventId}"]`,
    );
    await waitFor(() => expect(horizontalOption(3000)).not.toBeNull());
    expect(document.querySelector('.timeline-row')).toBeNull();
    fireEvent.click(horizontalOption(3000)!);
    fireEvent.click(await screen.findByRole('button', { name: 'Review-1 · WaitFor completed · #2 (older page)' }));

    await waitFor(() => expect(horizontalOption(2)?.className).toContain('jump-highlighted'));
    expect(horizontalOption(2)?.getAttribute('aria-selected')).toBe('true');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await waitFor(() => expect(horizontalOption(3000)?.getAttribute('aria-selected')).toBe('true'));
  });

  it('marks the event the panel shows on the horizontal Timeline, before and after keyboard moves', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(min-width: 761px)',
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }));
    const server = historyTestServer(sequentialStepHistory(30));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    const selectedOption = () => document.querySelector<HTMLElement>('[role="option"][aria-selected="true"]');
    // With no click, the panel shows the newest event, and the strip marks the same one.
    await waitFor(() => expect(selectedOption()).not.toBeNull());
    const newestEventId = Number(selectedOption()!.dataset.eventId);
    expect(selectedOption()!.className).toContain('selected');
    expect(document.querySelector('.sidebar-stack')?.textContent).toContain(`#${newestEventId}`);
    expect([...document.querySelectorAll('.htl-axis-label.selected')].map((label) => label.textContent))
      .toEqual([`#${newestEventId}`]);

    fireEvent.keyDown(screen.getByRole('listbox', { name: 'Timeline events, oldest on the left' }), { key: 'ArrowLeft' });
    await waitFor(() => expect(selectedOption()?.dataset.eventId).toBe(String(newestEventId - 1)));
    expect(selectedOption()!.className).toContain('selected');
    expect(document.querySelector('.htl-axis-label.selected')?.textContent).toBe(`#${newestEventId - 1}`);
  });

  it('renders the whole event panel below the horizontal Timeline, through its last section', async () => {
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query === '(min-width: 761px)',
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    }));
    const review = { stepExecutionId: 'Review-1', fromStepExecutionId: '__start__', stepType: 'Review' };
    const server = historyTestServer([sequentialStepHistory(1)[0], stepEvent(2, 'StepWaitForCompleted', review)]);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    const option = await waitFor(() => {
      const element = document.querySelector<HTMLElement>('[role="option"][data-event-id="2"]');
      expect(element).not.toBeNull();
      return element!;
    });
    fireEvent.click(option);
    const panel = document.querySelector<HTMLElement>('.run-content--horizontal-timeline .sidebar-stack');
    expect(panel).not.toBeNull();
    const headings = await waitFor(() => {
      const found = [...panel!.querySelectorAll('h4')].map((heading) => heading.textContent);
      expect(found).toContain('Context');
      return found;
    });
    const lastHeading = [...panel!.querySelectorAll('h4')].at(-1)!;
    expect(lastHeading.textContent).toBe(headings.at(-1));
    expect(lastHeading.closest('[hidden], [aria-hidden="true"]')).toBeNull();
    expect(panel!.style.maxHeight).toBe('');
  });

  it('switches the Events page size, reloads the Latest page, and remembers the choice', async () => {
    const server = historyTestServer(sequentialStepHistory(1505));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('1505'));
    expect(eventsRange()).toBe('Showing #506–#1505 · Page 1 of 2');
    const pageSize = screen.getByRole('combobox', { name: 'Page size' }) as HTMLSelectElement;
    expect(pageSize.value).toBe('1000');

    fireEvent.change(pageSize, { target: { value: '100' } });
    await waitFor(() => expect(eventsRange()).toBe('Showing #1406–#1505 · Page 1 of 16'));
    expect(timelineRowIDs()).toHaveLength(100);
    expect(window.localStorage.getItem('dex.run.events-page-size')).toBe('100');

    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(eventsRange()).toBe('Showing #1306–#1405 · Page 2 of 16'));

    // The oldest page pairs FlowStart with the events before the next numbered page.
    fireEvent.click(pageButton('Oldest page'));
    await waitFor(() => expect(timelineRowIDs()).toEqual(['5', '4', '3', '2', '1']));
    expect(eventsRange()).toBe('Showing #1–#5 · Page 16 of 16');

    fireEvent.change(screen.getByRole('combobox', { name: 'Page size' }), { target: { value: '1000' } });
    await waitFor(() => expect(eventsRange()).toBe('Showing #506–#1505 · Page 1 of 2'));
  });

  it('opens with the stored Events page size', async () => {
    vi.stubGlobal('localStorage', memoryStorage({
      'dex.run.selected-tab': 'timeline',
      'dex.run.events-page-size': '100',
    }));
    const server = historyTestServer(sequentialStepHistory(1505));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(eventsRange()).toBe('Showing #1406–#1505 · Page 1 of 16'));
    expect((screen.getByRole('combobox', { name: 'Page size' }) as HTMLSelectElement).value).toBe('100');
  });

  it('jumps to a related event and goes back with the 100-event page size', async () => {
    vi.stubGlobal('localStorage', memoryStorage({
      'dex.run.selected-tab': 'timeline',
      'dex.run.events-page-size': '100',
    }));
    const review = { stepExecutionId: 'Review-1', fromStepExecutionId: '__start__', stepType: 'Review' };
    const filler = Array.from({ length: 2997 }, (_, offset): FlowHistoryEvent => ({
      eventId: offset + 3,
      eventTime: '2026-10-24T18:00:00Z',
      type: 'ChannelExternalPublish',
      payload: { messages: [] },
      runAttempt: 1,
    }));
    const execute: FlowHistoryEvent = {
      ...stepEvent(3000, 'StepExecuteCompleted', review),
      payload: {
        context: { ...review, waitForCompletedEventId: '2', startedTime: '2026-10-24T19:05:02Z' },
        input: { conditionResults: {} },
      },
    };
    const server = historyTestServer([
      sequentialStepHistory(1)[0],
      stepEvent(2, 'StepWaitForCompleted', review),
      ...filler,
      execute,
    ]);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    expect(timelineRowIDs()).toHaveLength(100);
    fireEvent.click(timelineRow(3000));
    fireEvent.click(await screen.findByRole('button', {
      name: '↓ From Review-1 · WaitFor completed · #2 (older page)',
    }));

    await waitFor(() => expect(timelineRow(2).className).toContain('jump-highlighted'));
    expect(timelineRowIDs().length).toBeLessThanOrEqual(100);
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    expect(timelineRowIDs()).toHaveLength(100);
    expect(timelineRow(3000).className).toContain('selected');
  });

  it('shows the pruned range banner and a muted pruned stub on the oldest retained page', async () => {
    const retained = sequentialStepHistory(5000).filter((event) => event.eventId === 1 || event.eventId >= 4001);
    const server = historyTestServer(retained);
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('5000'));
    // Only FlowStart precedes the newest 1000 events, so the newest page is also the oldest.
    await screen.findByText('Events #2 to #4000 were removed by the history window; FlowStart is kept.');
    expect(eventsRange()).toBe('Showing #1–#5000 · Page 1 of 1');
    // One page needs no page buttons.
    expect(screen.queryByRole('group', { name: 'Event pages' })).toBeNull();
    expect(timelineRowIDs().at(-1)).toBe('1');
    expect(timelineRowIDs().at(-2)).toBe('4001');
    fireEvent.click(timelineRow(4001));

    const stub = await screen.findByText('↓ From Step-2000 · WaitFor completed (removed by the history window)');
    expect(stub.tagName).toBe('SPAN');
    expect(stub.querySelector('.timeline-pruned-badge')?.textContent).toBe('Pruned');
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
    fireEvent.click(pageButton('Older page'));

    await screen.findByText('Events #2 to #5000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().at(-2)).toBe('5001');
  });

  it('reclassifies a jump target that was pruned after the page loaded', async () => {
    const server = historyTestServer(crossPageHistory(4500, 6000, 6000).filter((event) => event.eventId === 1 || event.eventId >= 4001));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('6000'));
    fireEvent.click(timelineRow(6000));
    const stub = await screen.findByRole('button', { name: '↓ From Review-1 · WaitFor completed · #4500 (older page)' });
    server.events = server.events.filter((event) => event.eventId === 1 || event.eventId >= 5001);
    fireEvent.click(stub);

    const prunedStub = await screen.findByText('↓ From Review-1 · WaitFor completed · #4500 (removed by the history window)');
    expect(prunedStub.tagName).toBe('SPAN');
    expect(screen.queryByText(/is not in the retained history/)).toBeNull();
    expect(timelineRowIDs()[0]).toBe('6000');
  });

  it('reloads the older page on Back after a jump from it', async () => {
    const server = historyTestServer(crossPageHistory(2, 1500, 3000));
    vi.stubGlobal('fetch', pagedDex(server).fetch);
    renderRun();

    await waitFor(() => expect(timelineRowIDs()[0]).toBe('3000'));
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('2000'));
    fireEvent.click(timelineRow(1500));
    fireEvent.click(await screen.findByRole('button', { name: '↓ From Review-1 · WaitFor completed · #2 (older page)' }));
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
    const stub = await screen.findByText('↓ From Review-1 · WaitFor completed · #3000 (removed by the history window)');
    expect(stub.tagName).toBe('SPAN');
    fireEvent.click(pageButton('Older page'));
    await waitFor(() => expect(timelineRowIDs()[0]).toBe('5000'));

    // The page that starts at the oldest retained event also shows FlowStart, so it is the oldest page.
    await screen.findByText('Events #2 to #4000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().slice(-2)).toEqual(['4001', '1']);
    expect(eventsRange()).toBe('Showing #1–#5000 · Page 2 of 2');
    expect(pageButtonsDisabled()).toEqual([false, false, true, true]);
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
    await screen.findByText('↓ From Review-1 · WaitFor completed · #4000 (removed by the history window)');

    await screen.findByText('Events #2 to #5000 were removed by the history window; FlowStart is kept.');
    expect(timelineRowIDs().slice(-2)).toEqual(['9001', '1']);
    // Time travel skips event IDs, so the page count is not exact.
    expect(eventsRange()).toBe('Showing #1–#10000 · Page 1');
    // One page needs no page buttons.
    expect(screen.queryByRole('group', { name: 'Event pages' })).toBeNull();
  });
});


type StateReply = { historyWindowEvents: number; withBlob?: boolean } | 'fail';

interface HistorySettingsDex {
  /** Answers state reads at once while set; null holds each read until the test replies. */
  immediateStateReply: StateReply | null;
  /** Replies for runs other than run-1. */
  stateReplyByRunId: Record<string, StateReply>;
  runAttempt: number;
  heldStateReads: Array<(reply: StateReply) => void>;
  /** Holds history config updates while set, until the test releases them. */
  shouldHoldConfigUpdates: boolean;
  heldConfigUpdates: Array<() => void>;
  heldBlobLoads: Array<() => void>;
  configUpdates: unknown[];
}

function historySettingsDex(): HistorySettingsDex {
  const dex: HistorySettingsDex = {
    immediateStateReply: { historyWindowEvents: 500 },
    stateReplyByRunId: {},
    runAttempt: 1,
    heldStateReads: [],
    shouldHoldConfigUpdates: false,
    heldConfigUpdates: [],
    heldBlobLoads: [],
    configUpdates: [],
  };
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
  const stateResponse = (reply: StateReply) => {
    if (reply === 'fail') return json({ error: 'GetFlowState failed' }, 502);
    const attributes = reply.withBlob
      ? [{ key: 'note', value: { __dexBlobReference: { id: 'note-blob', kind: 'string' } } }]
      : [];
    return json({ ...emptyState, attributes, flowConfig: { historyWindowEvents: reply.historyWindowEvents } });
  };
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://dex.test');
    const runId = url.searchParams.get('runId') ?? 'run-1';
    if (url.pathname === '/api/flows/summary') return json({ ...summary(dex.runAttempt), runId });
    if (url.pathname === '/api/flows/state') {
      const runReply = dex.stateReplyByRunId[runId];
      if (runReply) return stateResponse(runReply);
      if (dex.immediateStateReply !== null) return stateResponse(dex.immediateStateReply);
      return new Promise<Response>((resolve) => {
        dex.heldStateReads.push((reply) => resolve(stateResponse(reply)));
      });
    }
    if (url.pathname === '/api/flows/config') {
      dex.configUpdates.push(JSON.parse(init?.body as string));
      if (dex.shouldHoldConfigUpdates) {
        await new Promise<void>((resolve) => dex.heldConfigUpdates.push(resolve));
      }
      return json({ updated: true });
    }
    if (url.pathname === '/api/blobs/load') {
      await new Promise<void>((resolve) => dex.heldBlobLoads.push(resolve));
      return json({ values: { 'string:note-blob': 'note text' } });
    }
    if (url.pathname === '/api/flows/history') return json(lineagePage(firstAttemptLineage, url.searchParams));
    if (isNewestEventProbe(url)) return json({ eventAvailable: true, availableInternalEventId: '3' });
    if (url.pathname === '/api/flows/wait') return new Promise<Response>(() => {});
    throw new Error(`unexpected request ${input}`);
  }));
  return dex;
}

function runPage(runId: string) {
  return (
    <MemoryRouter initialEntries={['/v1/flows/order-1/run-1']}>
      <PreferencesProvider>
        <RunDetailsPage
         
          flowId="order-1"
          runId={runId}
          runPath={(runID) => `/v1/flows/order-1/${runID}`}
        />
      </PreferencesProvider>
    </MemoryRouter>
  );
}

async function saveHistoryWindow(windowEvents: string) {
  fireEvent.click(await screen.findByRole('button', { name: 'Edit Retention window' }));
  fireEvent.change(screen.getByLabelText('Retention window (events)'), { target: { value: windowEvents } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
}

function settleStateLoads() {
  return new Promise((resolve) => setTimeout(resolve, 20));
}

describe('history settings on the run page', () => {
  it('keeps saved values when a state load started before the save resolves after it', async () => {
    const dex = historySettingsDex();
    renderRun();
    await screen.findByText('Last 500 events');

    dex.immediateStateReply = null;
    refreshByResumingLive();
    await waitFor(() => expect(dex.heldStateReads).toHaveLength(1));
    await saveHistoryWindow('800');
    await waitFor(() => expect(dex.heldStateReads).toHaveLength(2));
    expect(dex.configUpdates).toEqual([{ flowId: 'order-1', runId: 'run-1', historyWindowEvents: 800 }]);

    dex.heldStateReads[1]('fail');
    await screen.findByText('GetFlowState failed');
    dex.heldStateReads[0]({ historyWindowEvents: 500 });
    await settleStateLoads();

    expect(screen.getByText('Last 800 events')).toBeTruthy();
    expect(screen.queryByText('Last 500 events')).toBeNull();
  });

  it('keeps saved values across a tab switch while the refresh fails', async () => {
    const dex = historySettingsDex();
    renderRun();
    await screen.findByText('Last 500 events');

    dex.immediateStateReply = 'fail';
    await saveHistoryWindow('800');
    await screen.findByText('GetFlowState failed');
    expect(screen.getByText('Last 800 events')).toBeTruthy();

    fireEvent.click(screen.getByRole('tab', { name: 'Streams' }));
    await waitFor(() => expect(screen.queryByText('Last 800 events')).toBeNull());
    fireEvent.click(screen.getByRole('tab', { name: 'State' }));
    expect(await screen.findByText('Last 800 events')).toBeTruthy();
    expect(screen.queryByText('Last 500 events')).toBeNull();
  });

  it('shows the server values from a state load started after the save', async () => {
    const dex = historySettingsDex();
    renderRun();
    await screen.findByText('Last 500 events');

    // Another client wrote 1200 after this save, so the server value must replace the saved one.
    dex.immediateStateReply = { historyWindowEvents: 1200 };
    await saveHistoryWindow('800');

    expect(await screen.findByText('Last 1,200 events')).toBeTruthy();
    expect(screen.queryByText('Last 800 events')).toBeNull();
  });

  it('never lets an older state load overwrite a newer one', async () => {
    const dex = historySettingsDex();
    renderRun();
    await screen.findByText('Last 500 events');

    dex.immediateStateReply = null;
    refreshByResumingLive();
    await waitFor(() => expect(dex.heldStateReads).toHaveLength(1));
    refreshByResumingLive();
    await waitFor(() => expect(dex.heldStateReads).toHaveLength(2));

    dex.heldStateReads[1]({ historyWindowEvents: 900 });
    await screen.findByText('Last 900 events');
    dex.heldStateReads[0]({ historyWindowEvents: 700 });
    await settleStateLoads();

    expect(screen.getByText('Last 900 events')).toBeTruthy();
    expect(screen.queryByText('Last 700 events')).toBeNull();
  });

  it('ignores a save on one run that finishes after the page moved to another run', async () => {
    const dex = historySettingsDex();
    dex.stateReplyByRunId['run-2'] = { historyWindowEvents: 1500 };
    const { rerender } = render(runPage('run-1'));
    await screen.findByText('Last 500 events');

    dex.shouldHoldConfigUpdates = true;
    await saveHistoryWindow('800');
    await waitFor(() => expect(dex.heldConfigUpdates).toHaveLength(1));
    rerender(runPage('run-2'));
    await screen.findByText('Last 1,500 events');

    dex.immediateStateReply = 'fail';
    dex.heldConfigUpdates[0]();
    await settleStateLoads();

    expect(screen.getByText('Last 1,500 events')).toBeTruthy();
    expect(screen.queryByText('Last 800 events')).toBeNull();
  });

  it('ignores a save that finishes after the run attempt changed', async () => {
    const dex = historySettingsDex();
    renderRun();
    await screen.findByText('Last 500 events');

    dex.shouldHoldConfigUpdates = true;
    await saveHistoryWindow('800');
    await waitFor(() => expect(dex.heldConfigUpdates).toHaveLength(1));
    dex.runAttempt = 2;
    dex.immediateStateReply = { historyWindowEvents: 1500 };
    refreshByResumingLive();
    // The card stays in edit mode while its save is pending, so the new value shows as the placeholder.
    await screen.findByPlaceholderText('Unchanged: Last 1,500 events');

    dex.immediateStateReply = 'fail';
    dex.heldConfigUpdates[0]();
    await settleStateLoads();

    expect(screen.getByText('Last 1,500 events')).toBeTruthy();
    expect(screen.queryByText('Last 800 events')).toBeNull();
  });

  it('keeps saved values when a pre-save load finishes blob hydration after the save', async () => {
    const dex = historySettingsDex();
    dex.immediateStateReply = { historyWindowEvents: 500, withBlob: true };
    renderRun();
    await screen.findByText('Last 500 events');
    await waitFor(() => expect(dex.heldBlobLoads).toHaveLength(1));

    dex.immediateStateReply = 'fail';
    await saveHistoryWindow('800');
    await screen.findByText('GetFlowState failed');
    dex.heldBlobLoads[0]();
    await settleStateLoads();

    expect(screen.getByText('Last 800 events')).toBeTruthy();
    expect(screen.queryByText('Last 500 events')).toBeNull();
  });
});
