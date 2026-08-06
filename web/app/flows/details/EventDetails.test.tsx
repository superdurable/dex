// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import type { FlowHistoryEvent } from '@/lib/types';
import { STEP_EVENT_INPUT_UNAVAILABLE } from '@/lib/unavailable';
import { SemanticEventDetails } from './EventDetails';

function executeEvent(durability: number): FlowHistoryEvent {
  return {
    eventId: 12,
    eventTime: '2026-08-05T23:44:31.704507592Z',
    type: 'StepExecuteCompleted',
    payload: {
      input: {
        stepInput: { stringValue: 'charge' },
        conditionResults: { timerResults: [{ conditionId: 'timer-1' }] },
        attributes: [{ key: 'account', value: { stringValue: 'primary' } }],
        stepExecutionLocals: [{ key: 'attempted', value: { boolValue: true } }],
      },
      output: {
        stepDecision: { nextSteps: [{ stepType: 'complete' }] },
      },
      context: {
        stepExecutionId: 'charge-1',
        fromStepExecutionId: 'authorize-1',
        durability,
        finalAttempt: 2,
        startedTime: '2026-08-05T23:44:31.700000000Z',
        duration: '0.002844876s',
        methodOptions: {
          timeoutSeconds: 10,
          retryPolicy: { maximumAttempts: 3 },
        },
        isTransientStep: true,
      },
    },
  };
}

function renderDetails(event: FlowHistoryEvent): string {
  return renderToStaticMarkup(
    <PreferencesProvider>
      <SemanticEventDetails event={event} />
    </PreferencesProvider>,
  );
}

describe('selected step event details', () => {
  it.each([
    ['sync', 1],
    ['async', 2],
  ])('renders %s with the common input, output, and context structure', (_, durability) => {
    const markup = renderDetails(executeEvent(durability));
    const labels = ['Step input', 'Condition results', 'Attributes', 'Step locals', 'Output', 'Context'];
    const positions = labels.map((label) => markup.indexOf(label));

    expect(positions.every((position) => position >= 0)).toBe(true);
    expect(positions).toEqual([...positions].sort((left, right) => left - right));
    expect(markup).toContain('charge-1');
    expect(markup).toContain('authorize-1');
    expect(markup).toContain(`${durability === 1 ? 'sync' : 'async'}`);
    expect(markup).toContain('3ms');
    expect(markup).toContain('10s');
    expect(markup).not.toContain('Transient');
    expect(markup).not.toContain('previousAttempt');
  });

  it('renders the latest sync failure in context', () => {
    const event = executeEvent(1);
    event.payload.context = {
      ...event.payload.context as Record<string, unknown>,
      lastFailureInfo: {
        attempt: 1,
        message: 'worker unavailable',
        errorType: 'FLOW_ERROR_TYPE_WORKER_API_FAIL',
        retryState: 'RETRY_STATE_IN_PROGRESS',
        stackTrace: 'worker stack',
        details: {
          detail: 'sync retry failure',
          originalWorkerErrorType: 'UnavailableWorker',
          originalWorkerErrorDetail: 'try again',
          originalWorkerErrorStatus: 14,
        },
      },
    };
    const markup = renderDetails(event);

    expect(markup).toContain('Last failure');
    expect(markup).toContain('Attempt');
    expect(markup).toContain('Worker method failed');
    expect(markup).toContain('sync retry failure');
    expect(markup).toContain('UnavailableWorker');
    expect(markup).toContain('try again');
    expect(markup).toContain('worker stack');
    expect(markup).not.toContain('Previous attempts');
  });

  it('renders the terminal failure attempt with the same failure component', () => {
    const event = executeEvent(1);
    event.type = 'StepExecuteFailed';
    event.payload.output = {
      failure: {
        attempt: 2,
        message: 'retry exhausted',
        retryState: 'RETRY_STATE_MAXIMUM_ATTEMPTS_REACHED',
        details: { detail: 'terminal worker failure' },
      },
    };
    const markup = renderDetails(event);

    expect(markup).toContain('Failure');
    expect(markup).toContain('retry exhausted');
    expect(markup).toContain('terminal worker failure');
    expect(markup).toContain('2');
  });

  it('distinguishes an unavailable step input from a missing value blob', () => {
    const event = executeEvent(2);
    event.payload.input = { unavailable: true };
    const markup = renderDetails(event);

    expect(markup).toContain(STEP_EVENT_INPUT_UNAVAILABLE);
    expect(markup).not.toContain('Value blob unavailable');
  });
});
