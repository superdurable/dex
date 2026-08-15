// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
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

describe('selected event time travel', () => {
  it('offers a direct action on the selected event', () => {
    const markup = renderToStaticMarkup(
      <PreferencesProvider>
        <FlowStatePanel
          selectedEvent={event}
          history={[event]}
          parentFlowId="order-1"
          onTimeTravel={() => {}}
        />
      </PreferencesProvider>,
    );

    expect(markup).toContain('Time travel here');
  });

  it('opens with the selected Step execution and method prefilled', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <TimeTravelDialog
          open
          summary={summary}
          events={[event]}
          initialEvent={event}
          onClose={() => {}}
        />
      </MemoryRouter>,
    );

    expect(markup).toContain('<option value="4" selected="">Step execution ID</option>');
    expect(markup).toContain('value="ChargeOrder-2"');
    expect(markup).toContain('<option value="2" selected="">Execute</option>');
  });
});
