// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
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
  waitRequests: URLSearchParams[];
  timeTravel: () => void;
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
      const start = Number(url.searchParams.get('startInternalEventId'));
      const events = lineage.filter((event) => event.eventId >= start);
      return json({ events, nextPageToken: '', nextInternalEventId: Math.max(start, ...lineage.map((event) => event.eventId + 1)) });
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

function semanticEventCount(): string {
  const label = screen.getByText('Semantic events');
  return label.parentElement?.querySelector('b')?.textContent ?? '';
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
  it('reloads the whole history when time travel creates a new run attempt with the same run ID', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    const attemptSelect = await screen.findByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement;
    expect(attemptSelect.value).toBe('1');
    await waitFor(() => expect(semanticEventCount()).toBe('3'));
    await waitFor(() => expect(dex.waitRequests).toHaveLength(1));

    dex.timeTravel();

    await waitFor(() => expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2'));
    await waitFor(() => expect(semanticEventCount()).toBe('2'));
    expect(dex.historyRequests.map((params) => params.get('startInternalEventId'))).toEqual(['0', '0']);
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
    await waitFor(() => expect(semanticEventCount()).toBe('3'));
    expect(dex.historyRequests.at(-1)?.get('runAttempt')).toBe('1');
    expect(dex.waitRequests).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'View current attempt' }));
    await waitFor(() => expect(screen.queryByText(/Showing the history of run attempt 1/)).toBeNull());
    await waitFor(() => expect(semanticEventCount()).toBe('2'));
    expect(dex.historyRequests.at(-1)?.has('runAttempt')).toBe(false);
  });

  it('replaces the lineage on Refresh after another client time traveled while Live was off', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await waitFor(() => expect(semanticEventCount()).toBe('3'));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Live' }));
    dex.timeTravel();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));

    await waitFor(() => expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2'));
    await waitFor(() => expect(semanticEventCount()).toBe('2'));
    expect(dex.historyRequests.map((params) => params.get('startInternalEventId'))).toEqual(['0', '0']);
  });

  it('follows the current run attempt when the requested attempt does not exist', async () => {
    const dex = fakeDex();
    vi.stubGlobal('fetch', dex.fetch);
    dex.timeTravel();
    renderRun('/v1/flows/order-1/run-1?runAttempt=7');

    await waitFor(() => expect(semanticEventCount()).toBe('2'));
    expect(screen.queryByText(/Showing the history of run attempt/)).toBeNull();
    expect((screen.getByRole('combobox', { name: 'Run attempt' }) as HTMLSelectElement).value).toBe('2');
    expect(dex.historyRequests.every((params) => !params.has('runAttempt'))).toBe(true);
  });

  it('keeps the run chain for Servers without run attempts', async () => {
    const dex = fakeDex();
    dex.fetch.mockImplementation(async (input: string) => {
      const url = new URL(input, 'http://dex.test');
      if (url.pathname === '/api/flows/summary') return new Response(JSON.stringify({ ...summary(0), runAttempt: undefined }));
      if (url.pathname === '/api/flows/state') return new Response(JSON.stringify(emptyState));
      if (url.pathname === '/api/flows/history') {
        return new Response(JSON.stringify({ events: firstAttemptLineage.map((event) => ({ ...event, runAttempt: 0 })), nextPageToken: '', nextInternalEventId: 0 }));
      }
      throw new Error(`unexpected request ${input}`);
    });
    vi.stubGlobal('fetch', dex.fetch);
    renderRun();

    await screen.findByText('Run chain');
    expect(screen.queryByRole('combobox', { name: 'Run attempt' })).toBeNull();
  });
});
