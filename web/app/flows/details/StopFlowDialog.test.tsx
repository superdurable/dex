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
    expect(screen.getByText(/Cancels active Steps and completes the Flow once pending/)).toBeTruthy();
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
    const outputMessage = screen.getByText(/^Invalid JSON/);
    expect(outputMessage.getAttribute('aria-live')).toBe('polite');
    expect(outputField.getAttribute('aria-describedby')).toBe(outputMessage.id);
    expect(outputField.getAttribute('aria-invalid')).toBe('true');
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

  it('submits Force complete with its output', async () => {
    const fetchMock = vi.fn(async () => okResponse());
    vi.stubGlobal('fetch', fetchMock);
    const { onStopped, stopTypeSelect } = renderDialog();
    fireEvent.change(stopTypeSelect, { target: { value: '5' } });
    fireEvent.change(screen.getByPlaceholderText(outputPlaceholder), { target: { value: '"closed by operator"' } });
    fireEvent.click(screen.getByRole('button', { name: 'Stop flow' }));

    await waitFor(() => expect(onStopped).toHaveBeenCalled());
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      flowId: 'session-42',
      runId: 'run-1',
      stopType: 5,
      reason: '',
      output: '"closed by operator"',
    });
  });

  it('clears the output and error when it reopens after a close', () => {
    vi.stubGlobal('fetch', vi.fn(async () => okResponse()));
    const { rerender } = render(dialogAt(true));
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '4' } });
    fireEvent.change(screen.getByPlaceholderText(outputPlaceholder), { target: { value: '{"summary":' } });
    expect(screen.getByText(/^Invalid JSON/)).toBeTruthy();

    rerender(dialogAt(false));
    rerender(dialogAt(true));

    expect((screen.getByPlaceholderText(outputPlaceholder) as HTMLTextAreaElement).value).toBe('');
    expect(screen.queryByText(/^Invalid JSON/)).toBeNull();
  });

  it('clears the submitted output when it reopens after a stop', async () => {
    const fetchMock = vi.fn(async () => okResponse());
    vi.stubGlobal('fetch', fetchMock);
    let isOpen = true;
    const onClose = vi.fn(() => { isOpen = false; });
    const { rerender } = render(dialogAt(true, onClose));
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '4' } });
    fireEvent.change(screen.getByPlaceholderText(outputPlaceholder), { target: { value: '{"summary":"done"}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Stop flow' }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());

    rerender(dialogAt(isOpen, onClose));
    rerender(dialogAt(true, onClose));

    expect((screen.getByPlaceholderText(outputPlaceholder) as HTMLTextAreaElement).value).toBe('');
  });
});

const outputPlaceholder = '{"summary": "ended by operator"}';

function okResponse(): Response {
  return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function dialogAt(open: boolean, onClose: () => void = () => {}) {
  return <StopFlowDialog open={open} summary={summary} onClose={onClose} onStopped={() => {}} />;
}
