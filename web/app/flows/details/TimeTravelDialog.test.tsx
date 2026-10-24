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
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import type { FlowHistoryEvent, FlowSummary } from '@/lib/types';
import { FlowStatePanel } from './FlowStatePanel';
import { TimeTravelDialog } from './TimeTravelDialog';

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
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('selected event time travel', () => {
  it('does not duplicate the toolbar action in the selected event panel', () => {
    const markup = renderToStaticMarkup(
      <PreferencesProvider>
        <FlowStatePanel
          selectedEvent={event}
          history={[event]}
          parentFlowId="order-1"
        />
      </PreferencesProvider>,
    );

    expect(markup).not.toContain('Critical action');
    expect(markup).not.toContain('Review time travel');
  });

  it('opens with the selected Step execution and method prefilled', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <TimeTravelDialog
          open
          summary={summary}
          events={[event]}
          initialEvent={event}
          runAttempt={0}
          timezone="local"
          onClose={() => {}}
          onTimeTraveled={() => {}}
        />
      </MemoryRouter>,
    );

    expect(markup).toContain('<option value="4" selected="">Step execution ID</option>');
    expect(markup).toContain('value="ChargeOrder-2"');
    expect(markup).toContain('<option value="2" selected="">Execute</option>');
    expect(markup).toContain('creates a new run from');
  });

  it('describes a new run attempt when the Server keeps the run ID', () => {
    const markup = renderToStaticMarkup(
      <TimeTravelDialog
        open
        summary={{ ...summary, runAttempt: 2 }}
        events={[event]}
        initialEvent={null}
        runAttempt={1}
        timezone="UTC"
        onClose={() => {}}
        onTimeTraveled={() => {}}
      />,
    );

    expect(markup).toContain('New run attempt');
    expect(markup).toContain('Searching the history of run attempt 1.');
  });
});

describe('time travel requests', () => {
  it('sends the event time in RFC 3339 UTC and the viewed run attempt', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(
      JSON.stringify({ runId: 'run-1', runAttempt: 3 }),
      { status: 200 },
    ));
    vi.stubGlobal('fetch', fetchMock);
    const onTimeTraveled = vi.fn();
    const onClose = vi.fn();
    render(
      <TimeTravelDialog
        open
        summary={{ ...summary, runAttempt: 2 }}
        events={[event]}
        initialEvent={null}
        runAttempt={1}
        timezone="UTC"
        onClose={onClose}
        onTimeTraveled={onTimeTraveled}
      />,
    );

    fireEvent.change(screen.getByRole('combobox'), { target: { value: '2' } });
    fireEvent.change(screen.getByLabelText('Target (UTC)'), { target: { value: '2026-10-24T17:30:05' } });
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'replay the fix' } });
    fireEvent.click(screen.getByRole('button', { name: 'Time travel' }));

    await waitFor(() => expect(onTimeTraveled).toHaveBeenCalledWith({ runId: 'run-1', runAttempt: 3 }));
    expect(onClose).toHaveBeenCalled();
    expect(fetchMock.mock.calls[0][0]).toBe('/api/flows/time-travel');
    expect(JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string)).toEqual({
      flowId: 'order-1',
      runId: 'run-1',
      timeTravelType: 2,
      reason: 'replay the fix',
      historyEventTime: '2026-10-24T17:30:05.000Z',
      runAttempt: 1,
    });
  });

  it('reports a new run ID from a Server without run attempts', async () => {
    const fetchMock = vi.fn(async (_url: string, _init?: RequestInit) => new Response(
      JSON.stringify({ runId: 'run-2', runAttempt: 0 }),
      { status: 200 },
    ));
    vi.stubGlobal('fetch', fetchMock);
    const onTimeTraveled = vi.fn();
    render(
      <TimeTravelDialog
        open
        summary={summary}
        events={[event]}
        initialEvent={event}
        runAttempt={0}
        timezone="local"
        onClose={() => {}}
        onTimeTraveled={onTimeTraveled}
      />,
    );

    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'retry' } });
    fireEvent.click(screen.getByRole('button', { name: 'Time travel' }));

    await waitFor(() => expect(onTimeTraveled).toHaveBeenCalledWith({ runId: 'run-2', runAttempt: 0 }));
    const body = JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string);
    expect(body.runAttempt).toBeUndefined();
    expect(body.stepExecutionId).toBe('ChargeOrder-2');
  });
});
