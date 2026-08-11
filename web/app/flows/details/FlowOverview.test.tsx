// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import type { FlowState, FlowSummary } from '@/lib/types';
import { FlowOverview } from './FlowOverview';

describe('live Flow state failures', () => {
  it('shows the Java Worker stack expanded with a named gRPC status', () => {
    const summary: FlowSummary = {
      flowId: 'flow-1',
      runId: 'run-1',
      firstRunId: 'run-1',
      requestId: 'request-1',
      flowType: 'PaymentFlow',
      flowStatus: 'Running',
      flowStatusCode: 1,
      startTime: '2026-08-10T00:00:00Z',
      closeTime: null,
    };
    const state: FlowState = {
      flowConfig: {},
      attributes: [],
      activeStepExecutions: [{
        stepExecutionId: 'ChargeCard-1',
        fromStepExecutionId: '__start__',
        stepType: 'ChargeCard',
        phase: 'Active',
        stepExecutionLocals: [],
        timers: [],
        lastFailureInfo: {
          attempt: 2,
          message: 'payment processor failed',
          retryState: 'RETRY_STATE_IN_PROGRESS',
          details: {
            originalWorkerErrorType: 'PaymentProcessorException',
            originalWorkerErrorDetail: 'processor unavailable',
            originalWorkerErrorStatus: 13,
            originalWorkerErrorStackTrace: 'java stack trace',
          },
        },
      }],
      queuedSteps: [],
      pendingChannelMessages: {},
      completedSteps: [],
    };

    const markup = renderToStaticMarkup(
      <FlowOverview
        summary={summary}
        events={[]}
        state={state}
        selectedEvent={null}
      />,
    );

    expect(markup).toContain('Last failure');
    expect(markup).toContain('Retry scheduled');
    expect(markup).toContain('INTERNAL (13)');
    expect(markup).toContain('java stack trace');
    expect(markup).toContain('<details class="active-step-card" open="">');
    expect(markup).toContain('Collapse all');
    expect(markup).toContain('<details class="failure-stack" open="">');
  });
});
