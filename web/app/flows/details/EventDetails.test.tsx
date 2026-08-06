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

  it('distinguishes an unavailable step input from a missing value blob', () => {
    const event = executeEvent(2);
    event.payload.input = { unavailable: true };
    const markup = renderDetails(event);

    expect(markup).toContain(STEP_EVENT_INPUT_UNAVAILABLE);
    expect(markup).not.toContain('Value blob unavailable');
  });
});
