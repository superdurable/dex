// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowSummary } from '@/lib/types';
import { buildHistorySettingsUpdate, HistorySettingsCard } from './HistorySettingsCard';

const runningSummary: FlowSummary = {
  flowId: 'session-1',
  runId: 'run-1',
  firstRunId: 'run-1',
  requestId: 'request-1',
  flowType: 'SessionFlow',
  flowStatus: 'Running',
  flowStatusCode: 1,
  startTime: '2026-11-01T00:00:00Z',
  closeTime: null,
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('history settings card', () => {
  it('shows unset fields as the server default', () => {
    render(
      <HistorySettingsCard summary={runningSummary} flowConfig={{ stepDurability: 1 }} onSaved={vi.fn()} />,
    );
    expect(screen.getAllByText('Server default')).toHaveLength(2);
  });

  it('shows run overrides', () => {
    render(
      <HistorySettingsCard
        summary={runningSummary}
        flowConfig={{ historyWindowEvents: 2500, deleteExpiredHistoryObjects: false }}
        onSaved={vi.fn()}
      />,
    );
    expect(screen.getByText('2,500 events')).toBeTruthy();
    expect(screen.getByText('Off')).toBeTruthy();
  });

  it('offers no editing for a closed run', () => {
    render(
      <HistorySettingsCard
        summary={{ ...runningSummary, flowStatus: 'Completed', flowStatusCode: 2 }}
        flowConfig={undefined}
        onSaved={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: 'Edit' })).toBeNull();
    expect(screen.getByText(/only while this run is active/)).toBeTruthy();
  });

  it('sends only the edited field and refreshes the run state', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(
      JSON.stringify({ updated: true }),
      { status: 200 },
    ));
    vi.stubGlobal('fetch', fetchMock);
    const onSaved = vi.fn(async () => {});
    render(<HistorySettingsCard summary={runningSummary} flowConfig={{}} onSaved={onSaved} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    const save = screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('History window (events)'), { target: { value: '800' } });
    fireEvent.click(save);

    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(fetchMock.mock.calls[0][0]).toBe('/api/flows/config/history');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body as string)).toEqual({
      flowId: 'session-1',
      runId: 'run-1',
      historyWindowEvents: 800,
    });
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save' })).toBeNull());
  });

  it('shows a server rejection inline and stays in edit mode', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ error: 'history window exceeds history.maxWindowEvents', grpcCode: 3 }),
      { status: 400 },
    )));
    const onSaved = vi.fn(async () => {});
    render(<HistorySettingsCard summary={runningSummary} flowConfig={{}} onSaved={onSaved} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(screen.getByLabelText('Delete expired history objects'), { target: { value: 'on' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(await screen.findByText('history window exceeds history.maxWindowEvents')).toBeTruthy();
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy();
  });

  it('rejects an invalid window without a request', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<HistorySettingsCard summary={runningSummary} flowConfig={{}} onSaved={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(screen.getByLabelText('History window (events)'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    expect(screen.getByText('Enter a positive whole number of events.')).toBeTruthy();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('history settings update', () => {
  const current = { historyWindowEvents: 500, deleteExpiredHistoryObjects: true };

  it('leaves out unchanged values', () => {
    expect(buildHistorySettingsUpdate(current, '500', 'on')).toEqual({ update: {} });
    expect(buildHistorySettingsUpdate(current, ' ', 'unchanged')).toEqual({ update: {} });
  });

  it('includes each changed value', () => {
    expect(buildHistorySettingsUpdate(current, '600', 'off')).toEqual({
      update: { historyWindowEvents: 600, deleteExpiredHistoryObjects: false },
    });
  });

  it('rejects windows that are not positive 32-bit integers', () => {
    for (const text of ['0', '-1', '1.5', 'abc', '2147483648']) {
      expect(buildHistorySettingsUpdate(current, text, 'unchanged')).toHaveProperty('error');
    }
  });
});
