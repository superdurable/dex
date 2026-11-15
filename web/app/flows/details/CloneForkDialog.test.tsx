// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';
import { CloneForkDialog } from './CloneForkDialog';
import { FlowStatePanel } from './FlowStatePanel';

const event: FlowHistoryEvent = {
  eventId: 42,
  eventTime: '2026-08-15T12:00:00Z',
  type: 'StepExecuteCompleted',
  payload: { context: { stepExecutionId: 'ChargeOrder-2' } },
};

const summary: FlowSummary = {
  flowId: 'order-1',
  runId: 'run-1',
  firstRunId: 'run-1',
  requestId: 'request-1',
  flowType: 'OrderFlow',
  flowStatus: 'Running',
  flowStatusCode: 1,
  startTime: '2026-08-15T11:00:00Z',
  closeTime: null,
  runAttempt: 2,
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderDialog(overrides: Partial<Parameters<typeof CloneForkDialog>[0]> = {}) {
  const onCloneForked = vi.fn();
  const onClose = vi.fn();
  render(
    <CloneForkDialog
      open
      summary={summary}
      events={[event]}
      initialEvent={event}
      runAttempt={0}
      sourceTaskQueue="orders"
      isSourceRunning
      timezone="UTC"
      onClose={onClose}
      onCloneForked={onCloneForked}
      {...overrides}
    />,
  );
  return { onCloneForked, onClose };
}

function stubCloneFork() {
  const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(
    JSON.stringify({ flowId: 'order-1-copy', runId: 'copy-run', sourceRunId: 'run-1', sourceRunAttempt: 1, sourceEventId: '42' }),
    { status: 200 },
  ));
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function requestBody(fetchMock: ReturnType<typeof stubCloneFork>): Record<string, unknown> {
  return JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string);
}

describe('Clone fork from the selected event', () => {
  it('offers Clone fork beside Time travel for the same events', () => {
    const markup = renderToStaticMarkup(
      <PreferencesProvider>
        <FlowStatePanel
          selectedEvent={event}
          history={[event]}
          parentFlowId="order-1"
          onTimeTravelFromEvent={() => {}}
          onCloneForkFromEvent={() => {}}
        />
      </PreferencesProvider>,
    );
    expect(markup).toContain('Time travel from here…');
    expect(markup).toContain('Clone fork from here…');

    const startMarkup = renderToStaticMarkup(
      <PreferencesProvider>
        <FlowStatePanel
          selectedEvent={{ ...event, type: 'FlowStartedOrContinued', payload: {} }}
          history={[event]}
          parentFlowId="order-1"
          onCloneForkFromEvent={() => {}}
        />
      </PreferencesProvider>,
    );
    expect(startMarkup).not.toContain('Clone fork from here…');
  });

  it('copies the event ID point with the defaults and the viewed run attempt', async () => {
    const fetchMock = stubCloneFork();
    const { onCloneForked, onClose } = renderDialog({ runAttempt: 1 });

    expect((screen.getByRole('combobox', { name: 'Clone fork point' }) as HTMLSelectElement).value).toBe('event-id');
    expect((screen.getByLabelText('Target') as HTMLInputElement).value).toBe('42');
    expect((screen.getByRole('textbox', { name: /^New Flow ID/ }) as HTMLInputElement).value).toMatch(/^order-1-fork-\d{8}-\d{6}$/);
    expect((screen.getByRole('textbox', { name: /^Task queue/ }) as HTMLInputElement).value).toBe('orders');
    fireEvent.change(screen.getByRole('textbox', { name: /^New Flow ID/ }), { target: { value: 'order-1-copy' } });
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'try a fix' } });
    fireEvent.click(screen.getByRole('button', { name: 'Clone fork' }));

    await waitFor(() => expect(onCloneForked).toHaveBeenCalledWith({ flowId: 'order-1-copy', runId: 'copy-run' }));
    expect(onClose).toHaveBeenCalled();
    expect(fetchMock.mock.calls[0][0]).toBe('/api/flows/clone-fork');
    expect(requestBody(fetchMock)).toEqual({
      sourceFlowId: 'order-1',
      sourceRunId: 'run-1',
      flowId: 'order-1-copy',
      requestId: expect.any(String),
      pointType: 6,
      eventId: 42,
      idReusePolicy: 0,
      subFlowResetPolicy: 4,
      reason: 'try a fix',
      runAttempt: 1,
    });
  });

  it('sends a changed task queue and the chosen policies', async () => {
    const fetchMock = stubCloneFork();
    renderDialog();

    fireEvent.change(screen.getByRole('textbox', { name: /^Task queue/ }), { target: { value: 'orders-fork' } });
    fireEvent.change(screen.getByRole('combobox', { name: 'ID reuse policy' }), { target: { value: '4' } });
    fireEvent.change(screen.getByRole('combobox', { name: /^SubFlow reset policy/ }), { target: { value: '5' } });
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'try a fix' } });
    fireEvent.click(screen.getByRole('button', { name: 'Clone fork' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    expect(requestBody(fetchMock)).toMatchObject({ taskQueue: 'orders-fork', idReusePolicy: 4, subFlowResetPolicy: 5 });
  });
});

describe('Clone fork validation', () => {
  it('rejects the source Flow ID and an invalid task queue', () => {
    renderDialog();
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'try a fix' } });
    const button = screen.getByRole('button', { name: 'Clone fork' }) as HTMLButtonElement;
    expect(button.disabled).toBe(false);

    fireEvent.change(screen.getByRole('textbox', { name: /^New Flow ID/ }), { target: { value: 'order-1' } });
    expect(button.disabled).toBe(true);
    expect(screen.getByText(/Choose a Flow ID other than the source/)).toBeTruthy();

    fireEvent.change(screen.getByRole('textbox', { name: /^New Flow ID/ }), { target: { value: 'order-1-copy' } });
    fireEvent.change(screen.getByRole('textbox', { name: /^Task queue/ }), { target: { value: 'dex.reserved' } });
    expect(button.disabled).toBe(true);
  });

  it('offers the current state only while the source runs, without a run attempt', async () => {
    const fetchMock = stubCloneFork();
    renderDialog({ initialEvent: null, runAttempt: 1, sourceTaskQueue: '' });
    expect((screen.getByRole('combobox', { name: 'Clone fork point' }) as HTMLSelectElement).value).toBe('current');
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'branch' } });
    fireEvent.click(screen.getByRole('button', { name: 'Clone fork' }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const body = requestBody(fetchMock);
    expect(body.pointType).toBe(1);
    expect(body.runAttempt).toBeUndefined();
    expect(body.taskQueue).toBeUndefined();

    cleanup();
    renderDialog({ initialEvent: null, isSourceRunning: false });
    const point = screen.getByRole('combobox', { name: 'Clone fork point' }) as HTMLSelectElement;
    expect(point.value).toBe('beginning');
    expect([...point.options].map((option) => option.value)).not.toContain('current');
  });
});
