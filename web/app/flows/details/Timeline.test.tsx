// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import type { FlowHistoryEvent } from '@/lib/types';
import { previousRunLink, Timeline } from './Timeline';

const started: FlowHistoryEvent = {
  eventId: 1,
  eventTime: '2026-10-24T17:00:00Z',
  type: 'FlowStartedOrContinued',
  payload: {},
  runAttempt: 1,
};

const zdFork: FlowHistoryEvent = {
  eventId: 5,
  eventTime: '2026-10-24T17:00:05Z',
  type: 'TimeTravelFork',
  payload: { previousRunId: 'run-1', previousRunAttempt: 1, targetRunAttempt: 1, targetEventId: '2' },
  runAttempt: 2,
};

describe('Timeline run attempts', () => {
  it('links a fork to the previous run attempt when the run ID did not change', () => {
    expect(previousRunLink('order-1', zdFork)).toEqual({ search: '?runAttempt=1' });
    expect(previousRunLink('order-1', {
      ...zdFork,
      payload: { previousRunId: 'run-0' },
    })).toBe('/v1/flows/order-1/run-0');
    expect(previousRunLink('order-1', started)).toBeNull();
  });

  it('labels events with their run attempt when the lineage spans attempts', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter initialEntries={['/v1/flows/order-1/run-1']}>
        <PreferencesProvider>
          <Timeline flowId="order-1" events={[started, zdFork]} selectedEvent={null} onSelectEvent={() => {}} />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect(markup).toContain('Run attempt <b>2</b>');
    expect(markup).toContain('Run attempt <b>1</b>');
    expect(markup).toContain('href="/v1/flows/order-1/run-1?runAttempt=1"');
  });

  it('omits run attempt labels within one attempt', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <PreferencesProvider>
          <Timeline flowId="order-1" events={[started]} selectedEvent={null} onSelectEvent={() => {}} />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect(markup).not.toContain('Run attempt');
  });

  it('numbers events by event ID, so numbers stay stable across pages and pruning', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <PreferencesProvider>
          <Timeline
            flowId="order-1"
            events={[started, { ...zdFork, eventId: 4001, runAttempt: 1 }]}
            selectedEvent={null}
            onSelectEvent={() => {}}
            prunedEventIdRange={{ firstEventId: 2, lastEventId: 4000 }}
          />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    expect(markup).toContain('#4001');
    expect(markup).not.toContain('>#2<');
    expect(markup).toContain('Events #2 to #4000 were removed by the history window; FlowStart is kept.');
  });
});

describe('Timeline row density', () => {
  const execute = (eventId: number, eventTime: string, context: Record<string, unknown>): FlowHistoryEvent => ({
    eventId,
    eventTime,
    type: 'StepExecuteCompleted',
    payload: { context: { stepExecutionId: `Charge-${eventId}`, stepType: 'Charge', ...context } },
    runAttempt: 1,
  });

  function renderRows(events: FlowHistoryEvent[], runStepDurability?: unknown) {
    return renderToStaticMarkup(
      <MemoryRouter>
        <PreferencesProvider>
          <Timeline
            flowId="order-1"
            events={events}
            selectedEvent={null}
            onSelectEvent={() => {}}
            runStepDurability={runStepDurability}
          />
        </PreferencesProvider>
      </MemoryRouter>,
    );
  }

  it('keeps the event type out of the row and shows only informative chips', () => {
    const markup = renderRows([
      execute(2, '2026-10-24T17:00:01Z', { durability: 1, finalAttempt: 1 }),
      execute(3, '2026-10-24T17:00:01Z', { durability: 2, finalAttempt: 3 }),
    ], 1);
    expect(markup).not.toContain('class="event-type');
    expect(markup).toContain('title="StepExecuteCompleted"');
    expect(markup.match(/Durability <b>/g)).toHaveLength(1);
    expect(markup).toContain('Durability <b>async</b>');
    expect(markup).toContain('Attempt <b>3</b>');
    expect(markup).not.toContain('Attempt <b>1</b>');
  });

  it('shows an absolute time only when it changes from the row above', () => {
    const markup = renderRows([
      execute(2, '2026-10-24T17:00:01Z', {}),
      execute(3, '2026-10-24T17:00:01Z', {}),
      execute(4, '2026-10-24T17:05:00Z', {}),
    ]);
    expect(markup.match(/<b class="sr-only">/g)).toHaveLength(1);
  });

  it.each([undefined, 0, 'STEP_DURABILITY_UNSPECIFIED'])('treats a run durability of %s as the sync default', (runDurability) => {
    const markup = renderRows([execute(2, '2026-10-24T17:00:01Z', { durability: 1 })], runDurability);
    expect(markup).not.toContain('Durability <b>');
    const asyncMarkup = renderRows([execute(3, '2026-10-24T17:00:01Z', { durability: 2 })], runDurability);
    expect(asyncMarkup).toContain('Durability <b>async</b>');
  });
});
