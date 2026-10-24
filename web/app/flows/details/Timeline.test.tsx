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
});
