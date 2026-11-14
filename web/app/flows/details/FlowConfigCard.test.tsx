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
import { buildHistorySettingsUpdate, FlowConfigCard, flowConfigFieldHints } from './FlowConfigCard';

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

describe('Flow config card', () => {
  it('shows each field in plain words, with unset fields as the server default', () => {
    render(<FlowConfigCard summary={runningSummary} flowConfig={{ stepDurability: 1 }} onSaved={vi.fn()} />);
    const value = (label: string) => screen.getByText(label, { selector: 'dt' }).nextElementSibling?.textContent;
    expect(value('Retention window')).toContain('Server default');
    expect(value('Step durability')).toContain('Sync');
    expect(value('Active step search')).toContain('Server default');
    expect(value('Attribute stores')).toContain('Server default');
    expect(value('Offload history')).toBe('On (default)');
    // Every field explains what it does for the run, then whether it can change.
    expect(screen.getAllByRole('img').map((hint) => hint.getAttribute('aria-label'))).toEqual([
      flowConfigFieldHints.historyWindowEvents,
      flowConfigFieldHints.stepDurability,
      flowConfigFieldHints.activeStepSearchMode,
      flowConfigFieldHints.attributeStoreNames,
      flowConfigFieldHints.offloadHistory,
    ]);
    expect(flowConfigFieldHints.offloadHistory).toMatch(/^Whether Dex moves this run's history to object storage/);
    expect(flowConfigFieldHints.historyWindowEvents).toMatch(/can be changed but not cleared\.$/);
  });

  it('shows run overrides and hides fields with no meaning to a reader', () => {
    render(
      <FlowConfigCard
        summary={runningSummary}
        flowConfig={{
          historyWindowEvents: 2500,
          stepDurability: 2,
          activeStepSearchMode: 2,
          attributeStoreNames: {},
          offloadHistory: false,
          deleteExpiredHistoryObjects: false,
          workerTarget: { isPullMode: true, taskQueue: 'gpu' },
        }}
        onSaved={vi.fn()}
      />,
    );
    expect(screen.getByText('Last 2,500 events')).toBeTruthy();
    expect(screen.getByText('Async')).toBeTruthy();
    expect(screen.getByText('Steps with WaitFor')).toBeTruthy();
    expect(screen.getByText('None, synchronization off')).toBeTruthy();
    expect(screen.getByText('Off', { selector: '.flow-config-value' })).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/gpu|isPullMode|workerTarget|Worker target|delete/i);
  });

  it('offers Edit only for the fields UpdateFlowConfig applies', () => {
    render(<FlowConfigCard summary={runningSummary} flowConfig={{}} onSaved={vi.fn()} />);
    expect(screen.getAllByRole('button', { name: /^Edit / }).map((button) => button.getAttribute('aria-label'))).toEqual([
      'Edit Retention window',
      'Edit Step durability',
      'Edit Active step search',
      'Edit Attribute stores',
    ]);
    expect(screen.getByRole('img', { name: flowConfigFieldHints.offloadHistory })).toBeTruthy();
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
    expect(screen.getByRole('button', { name: 'Edit Retention window' })).toBeTruthy();
    expect(screen.getAllByText('Server default')).toHaveLength(4);
  });

  it('shows a closed run\'s config read-only', () => {
    render(
      <FlowConfigCard
        summary={{ ...runningSummary, flowStatus: 'Completed', flowStatusCode: 2 }}
        flowConfig={{ historyWindowEvents: 300, stepDurability: 2 }}
        onSaved={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button', { name: /Edit/ })).toBeNull();
    expect(screen.getByText('Last 300 events')).toBeTruthy();
    expect(screen.getByText(/run is closed/)).toBeTruthy();
  });

  it('says when the config is not loaded', () => {
    render(<FlowConfigCard summary={runningSummary} flowConfig={undefined} onSaved={vi.fn()} />);
    expect(screen.queryByRole('button', { name: /Edit/ })).toBeNull();
    expect(screen.getByText(/configuration is not loaded/)).toBeTruthy();
  });

  it('sends only the edited window and refreshes the run state', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(
      JSON.stringify({ updated: true }),
      { status: 200 },
    ));
    vi.stubGlobal('fetch', fetchMock);
    const onSaved = vi.fn(async () => {});
    render(<FlowConfigCard summary={runningSummary} flowConfig={{}} onSaved={onSaved} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit Retention window' }));
    const save = screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Retention window (events)'), { target: { value: '800' } });
    fireEvent.click(save);

    await waitFor(() => expect(onSaved).toHaveBeenCalledWith({ historyWindowEvents: 800 }, runningSummary));
    expect(fetchMock.mock.calls[0][0]).toBe('/api/flows/config');
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(init.method).toBe('PUT');
    expect(JSON.parse(init.body as string)).toEqual({ flowId: 'session-1', runId: 'run-1', historyWindowEvents: 800 });
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Save' })).toBeNull());
  });

  it('edits step durability, active step search and Attribute stores one at a time', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(
      JSON.stringify({ updated: true }),
      { status: 200 },
    ));
    vi.stubGlobal('fetch', fetchMock);
    const onSaved = vi.fn(async () => {});
    render(
      <FlowConfigCard summary={runningSummary} flowConfig={{ attributeStoreNames: { names: ['primary'] } }} onSaved={onSaved} />,
    );
    const lastBody = () => JSON.parse(fetchMock.mock.calls.at(-1)![1]!.body as string);
    const save = () => screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;

    fireEvent.click(screen.getByRole('button', { name: 'Edit Step durability' }));
    expect(screen.queryByRole('button', { name: 'Edit Active step search' })).toBeNull();
    // An unset select opens with nothing chosen, so Save waits for a choice.
    expect((screen.getByLabelText('Step durability') as HTMLSelectElement).value).toBe('');
    expect(save().disabled).toBe(true);
    fireEvent.change(screen.getByLabelText('Step durability'), { target: { value: '2' } });
    fireEvent.click(save());
    await waitFor(() => expect(onSaved).toHaveBeenLastCalledWith({ stepDurability: 2 }, runningSummary));
    expect(lastBody()).toEqual({ flowId: 'session-1', runId: 'run-1', stepDurability: 2 });

    fireEvent.click(await screen.findByRole('button', { name: 'Edit Active step search' }));
    fireEvent.change(screen.getByLabelText('Active step search'), { target: { value: '3' } });
    fireEvent.click(save());
    await waitFor(() => expect(onSaved).toHaveBeenLastCalledWith({ activeStepSearchMode: 3 }, runningSummary));

    fireEvent.click(await screen.findByRole('button', { name: 'Edit Attribute stores' }));
    const names = screen.getByLabelText('Attribute store names (comma-separated)') as HTMLInputElement;
    expect(names.value).toBe('primary');
    expect(save().disabled).toBe(true);
    // Clearing the box is not a way to turn sync off.
    fireEvent.change(names, { target: { value: '' } });
    expect(save().disabled).toBe(true);
    expect(screen.getByText('Enter at least one store name, or choose Turn sync off.')).toBeTruthy();
    fireEvent.click(screen.getByLabelText('Turn sync off for this run'));
    expect(screen.getByText(/stop syncing to any store/)).toBeTruthy();
    fireEvent.click(save());
    await waitFor(() => expect(onSaved).toHaveBeenLastCalledWith({ attributeStoreNames: { names: [] } }, runningSummary));
    expect(lastBody()).toEqual({ flowId: 'session-1', runId: 'run-1', attributeStoreNames: [] });
  });

  it('opens each editor on the current value and keeps Save off until it changes', () => {
    render(
      <FlowConfigCard
        summary={runningSummary}
        flowConfig={{ historyWindowEvents: 5000, stepDurability: 1, activeStepSearchMode: 2 }}
        onSaved={vi.fn()}
      />,
    );
    const save = () => screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement;
    fireEvent.click(screen.getByRole('button', { name: 'Edit Retention window' }));
    const windowInput = screen.getByLabelText('Retention window (events)') as HTMLInputElement;
    expect(windowInput.value).toBe('5000');
    expect(save().disabled).toBe(true);
    expect(screen.getByText(/Current value: Last 5,000 events\. A saved value cannot return to Server default later\./)).toBeTruthy();
    fireEvent.change(windowInput, { target: { value: '800' } });
    expect(save().disabled).toBe(false);
    expect(screen.getByText(/smaller than 5,000 events, so older events are deleted for good/)).toBeTruthy();
    fireEvent.change(windowInput, { target: { value: '9000' } });
    expect(screen.queryByText(/deleted for good/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    fireEvent.click(screen.getByRole('button', { name: 'Edit Step durability' }));
    expect((screen.getByLabelText('Step durability') as HTMLSelectElement).value).toBe('1');
    expect(save().disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    fireEvent.click(screen.getByRole('button', { name: 'Edit Active step search' }));
    expect((screen.getByLabelText('Active step search') as HTMLSelectElement).value).toBe('2');
    expect(save().disabled).toBe(true);
  });

  it('lays a row being edited across the card, below its name', () => {
    render(<FlowConfigCard summary={runningSummary} flowConfig={{}} onSaved={vi.fn()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Edit Retention window' }));
    const row = screen.getByLabelText('Retention window (events)').closest('dd')!.parentElement!;
    expect(row.className).toBe('is-editing');
    expect(row.querySelector('dt')?.textContent).toContain('Retention window');
  });

  it('shows a server rejection inline and stays in edit mode', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      JSON.stringify({ error: 'history window exceeds history.maxWindowEvents', grpcCode: 3 }),
      { status: 400 },
    )));
    const onSaved = vi.fn(async () => {});
    render(<FlowConfigCard summary={runningSummary} flowConfig={{}} onSaved={onSaved} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit Retention window' }));
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
    render(<FlowConfigCard summary={runningSummary} flowConfig={{}} onSaved={vi.fn()} />);

    fireEvent.click(screen.getByRole('button', { name: 'Edit Retention window' }));
    fireEvent.change(screen.getByLabelText('Retention window (events)'), { target: { value: '0' } });

    expect(screen.getByText('Enter a positive whole number of events.')).toBeTruthy();
    const windowInput = screen.getByLabelText('Retention window (events)');
    expect(windowInput.getAttribute('aria-invalid')).toBe('true');
    const notes = document.getElementById(windowInput.getAttribute('aria-describedby')!);
    expect(notes?.textContent).toContain('Enter a positive whole number of events.');
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
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
