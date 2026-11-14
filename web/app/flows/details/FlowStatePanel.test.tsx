// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import type { TimelineRelatedEvent } from '@/lib/timeline';
import type { FlowHistoryEvent } from '@/lib/types';
import { FlowStatePanel } from './FlowStatePanel';

afterEach(cleanup);

const syncBatch: FlowHistoryEvent = {
  eventId: 3097,
  eventTime: '2026-11-12T22:53:55Z',
  type: 'StepExecuteCompleted',
  payload: {
    input: { stepInput: { stringValue: 'batch 3097' } },
    output: { stepDecision: { nextSteps: [{ stepType: 'SyncBatch' }] } },
    context: {
      stepType: 'SyncBatch',
      stepExecutionId: 'SyncBatch-3097',
      durability: 1,
      finalAttempt: 2,
      pullRouting: { servedByAppWorkerId: 'worker-local-1' },
    },
  },
};

const related: TimelineRelatedEvent[] = [{
  id: 'source-3096',
  kind: 'lineage',
  relation: 'source',
  direction: 'older',
  location: 'loaded',
  stepExecutionId: 'SyncBatch-3095',
  eventId: 3096,
  description: 'SyncBatch-3095 decision',
  elapsedDurationMs: null,
}];

function renderPanel(onSelectRelatedEvent = vi.fn()) {
  render(
    <PreferencesProvider>
      <MemoryRouter>
        <FlowStatePanel
          history={[syncBatch]}
          onSelectRelatedEvent={onSelectRelatedEvent}
          parentFlowId="sync-1"
          relatedEvents={related}
          selectedEvent={syncBatch}
        />
      </MemoryRouter>
    </PreferencesProvider>,
  );
  return onSelectRelatedEvent;
}

describe('selected event panel', () => {
  it('puts relations on one line under the title, each a link to its event', () => {
    const onSelectRelatedEvent = renderPanel();
    const line = screen.getByRole('list', { name: 'Related events' });
    expect(line.closest('header')).not.toBeNull();
    expect(line.textContent).toBe('Source executionfrom SyncBatch-3095 decision, event #3096');
    fireEvent.click(screen.getByRole('button', { name: 'from SyncBatch-3095 decision, event #3096' }));
    expect(onSelectRelatedEvent).toHaveBeenCalledWith(3096);
  });

  it('leads with Input and Output, then shows Context in groups', () => {
    renderPanel();
    const headings = [...document.querySelectorAll('.semantic-event h4')].map((heading) => heading.textContent);
    expect(headings).toEqual(['Input', 'Output', 'Context']);
    const groups = [...document.querySelectorAll('.semantic-context-groups h5')].map((heading) => heading.textContent);
    expect(groups).toEqual(['Attempt', 'Worker and retry']);
  });


  it('switches the payload to raw JSON from one button in the header', () => {
    renderPanel();
    const json = screen.getByRole('button', { name: 'JSON' });
    expect(json.getAttribute('aria-pressed')).toBe('false');
    expect(screen.queryByRole('button', { name: 'Raw JSON' })).toBeNull();
    fireEvent.click(json);
    expect(json.getAttribute('aria-pressed')).toBe('true');
    expect(document.querySelector('.raw-event-json')?.textContent).toContain('"stepExecutionId": "SyncBatch-3097"');
    fireEvent.click(json);
    expect(document.querySelector('.raw-event-json')).toBeNull();
  });

});
