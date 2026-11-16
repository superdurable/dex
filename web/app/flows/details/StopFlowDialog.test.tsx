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
import { StopFlowDialog } from './StopFlowDialog';

const summary: FlowSummary = {
  flowId: 'session-42',
  runId: 'run-1',
  firstRunId: 'run-1',
  requestId: 'request-1',
  flowType: 'SessionFlow',
  flowStatus: 'Running',
  flowStatusCode: 1,
  startTime: '2026-11-15T11:00:00Z',
  closeTime: null,
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderDialog() {
  const onStopped = vi.fn();
  render(<StopFlowDialog open summary={summary} onClose={() => {}} onStopped={onStopped} />);
  return { onStopped, stopTypeSelect: screen.getByRole('combobox') };
}

describe('StopFlowDialog', () => {
  it('defaults to cancel and shows the output field only for the complete types', () => {
    const { stopTypeSelect } = renderDialog();

    expect((stopTypeSelect as HTMLSelectElement).value).toBe('1');
    expect(screen.queryByText('Output (optional JSON)')).toBeNull();
    expect(screen.getByText('Reason (optional)')).toBeTruthy();

    fireEvent.change(stopTypeSelect, { target: { value: '4' } });
    expect(screen.getByText(/after active Steps finish/)).toBeTruthy();
    expect(screen.getByText('Output (optional JSON)')).toBeTruthy();
    expect(screen.queryByText(/^Reason/)).toBeNull();

    fireEvent.change(stopTypeSelect, { target: { value: '5' } });
    expect(screen.getByText(/at once and cancels active Steps/)).toBeTruthy();
    expect(screen.getByText('Output (optional JSON)')).toBeTruthy();

    fireEvent.change(stopTypeSelect, { target: { value: '2' } });
    expect(screen.queryByText('Output (optional JSON)')).toBeNull();
  });

  it('blocks invalid output JSON and sends valid output as raw text', async () => {
    const fetchMock = vi.fn(async () => new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }));
    vi.stubGlobal('fetch', fetchMock);
    const { onStopped, stopTypeSelect } = renderDialog();
    fireEvent.change(stopTypeSelect, { target: { value: '4' } });
    const outputField = screen.getByPlaceholderText('{"summary": "ended by operator"}');
    const submitButton = screen.getByRole('button', { name: 'Stop flow' }) as HTMLButtonElement;

    fireEvent.change(outputField, { target: { value: '{"summary":' } });
    expect(screen.getByText(/^Invalid JSON/)).toBeTruthy();
    expect(submitButton.disabled).toBe(true);

    fireEvent.change(outputField, { target: { value: ' {"count": 9007199254740993} ' } });
    expect(submitButton.disabled).toBe(false);
    fireEvent.click(submitButton);

    await waitFor(() => expect(onStopped).toHaveBeenCalled());
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      flowId: 'session-42',
      runId: 'run-1',
      stopType: 4,
      reason: '',
      output: '{"count": 9007199254740993}',
    });
  });
});
