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
import type { FlowState, FlowSummary } from '@/lib/types';
import { FlowOverview } from './FlowOverview';
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
    expect(screen.getAllByText('Server default')).toHaveLength(1);
    expect(screen.getByRole('img', { name: 'Older events are deleted.' })).toBeTruthy();
    expect(screen.queryByText(/Delete expired history/)).toBeNull();
  });

  it('shows run overrides', () => {
    render(
      <HistorySettingsCard
        summary={runningSummary}
        flowConfig={{ historyWindowEvents: 2500, deleteExpiredHistoryObjects: false }}
        onSaved={vi.fn()}
      />,
    );
    expect(screen.getByText('Retention window')).toBeTruthy();
    expect(screen.getByText('Last 2,500 events')).toBeTruthy();
    expect(screen.queryByText('Off')).toBeNull();
  });

  it('treats a running run without a stored config as server defaults', () => {
    const state = {
      flowConfig: null,
      attributes: [],
      activeStepExecutions: [],
      queuedSteps: [],
      pendingChannelMessages: {},
      completedSteps: [],
    } as unknown as FlowState;
    render(
      <FlowOverview summary={runningSummary} events={[]} state={state} onHistorySettingsSaved={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: 'Edit' })).toBeTruthy();
    expect(screen.getAllByText('Server default')).toHaveLength(1);
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
    fireEvent.change(screen.getByLabelText('Retention window (events)'), { target: { value: '800' } });
    fireEvent.click(save);

    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ historyWindowEvents: 800 }, runningSummary));
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
    fireEvent.change(screen.getByLabelText('Retention window (events)'), { target: { value: '900000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toBe('history window exceeds history.maxWindowEvents');
    expect(screen.getByLabelText('Retention window (events)').getAttribute('aria-invalid')).toBeNull();
    expect(onSaved).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy();
  });

  it('rejects an invalid window without a request', () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    render(<HistorySettingsCard summary={runningSummary} flowConfig={{}} onSaved={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(screen.getByLabelText('Retention window (events)'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    const alert = screen.getByRole('alert');
    expect(alert.textContent).toBe('Enter a positive whole number of events.');
    const windowInput = screen.getByLabelText('Retention window (events)');
    expect(windowInput.getAttribute('aria-invalid')).toBe('true');
    expect(windowInput.getAttribute('aria-describedby')).toBe(alert.id);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('history settings update', () => {
  const current = { historyWindowEvents: 500 };

  it('leaves out unchanged values', () => {
    expect(buildHistorySettingsUpdate(current, '500')).toEqual({ update: {} });
    expect(buildHistorySettingsUpdate(current, ' ')).toEqual({ update: {} });
  });

  it('includes a changed window and never the deletion switch', () => {
    expect(buildHistorySettingsUpdate(current, '600')).toEqual({
      update: { historyWindowEvents: 600 },
    });
  });

  it('rejects windows that are not positive 32-bit integers', () => {
    for (const text of ['0', '-1', '1.5', 'abc', '2147483648']) {
      expect(buildHistorySettingsUpdate(current, text)).toHaveProperty('error');
    }
  });
});
