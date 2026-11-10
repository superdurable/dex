// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StepGraphNode } from '@/lib/types';
import { MemoryRouter } from 'react-router-dom';
import { PreferencesProvider } from '@/app/providers';
import { StepNodeLabel } from './ExecutionGraph';
import { FlowStatePanel } from './FlowStatePanel';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const completedNode: StepGraphNode = {
  id: 'ChargeCard-1',
  label: 'ChargeCard',
  kind: 'step',
  status: 'Completed',
  execute: { eventId: 3, eventTime: null, type: 'StepExecuteCompleted', payload: {} },
};

describe('Execution graph Step node', () => {
  it('shows the Step status as visible text beside its icon', () => {
    render(<StepNodeLabel node={completedNode} onSelect={vi.fn()} />);
    const status = screen.getAllByText('Completed')[0];
    expect(status.closest('.graph-step-meta')).not.toBeNull();
    expect(status.className).not.toContain('sr-only');
    expect(screen.getByText('ChargeCard-1')).toBeTruthy();
  });

  it('names a focused method by its method and status', () => {
    render(<StepNodeLabel node={completedNode} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute Completed' });
    execute.focus();
    expect(document.activeElement).toBe(execute);
    expect(execute.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('shows each waited Channel name on its own line with the full list as a tooltip', () => {
    const node: StepGraphNode = {
      ...completedNode,
      waitFor: {
        eventId: 2,
        eventTime: null,
        type: 'StepWaitForCompleted',
        payload: {
          output: {
            waitForCondition: {
              channelConditions: [{ channelName: 'TurnControlRequests' }, { channelName: 'QueuedUserMessages' }],
            },
          },
        },
      },
    };
    render(<StepNodeLabel node={node} onSelect={vi.fn()} />);
    const names = screen.getByText('TurnControlRequests').parentElement;
    expect(names?.className).toBe('graph-condition-names');
    expect([...(names?.children ?? [])].map((child) => child.textContent)).toEqual(['TurnControlRequests', 'QueuedUserMessages']);
    expect(names?.closest('i')?.getAttribute('title')).toBe('TurnControlRequests, QueuedUserMessages');
  });
});

describe('Execution graph method selection', () => {
  it('marks the method that recorded the selected event', () => {
    const node: StepGraphNode = {
      ...completedNode,
      waitFor: { eventId: 2, eventTime: null, type: 'StepWaitForCompleted', payload: {} },
    };
    const { rerender } = render(<StepNodeLabel node={node} onSelect={vi.fn()} selectedEventId={2} />);
    const waitFor = screen.getByRole('button', { name: /^WaitFor/ });
    const execute = screen.getByRole('button', { name: 'Execute Completed' });
    expect(waitFor.className).toContain('is-selected');
    expect(waitFor.getAttribute('aria-pressed')).toBe('true');
    expect(execute.getAttribute('aria-pressed')).toBe('false');

    // Selecting from elsewhere, such as Timeline, moves the selected state.
    rerender(<StepNodeLabel node={node} onSelect={vi.fn()} selectedEventId={3} />);
    expect(screen.getByRole('button', { name: 'Execute Completed' }).className).toContain('is-selected');
    expect(screen.getByRole('button', { name: /^WaitFor/ }).getAttribute('aria-pressed')).toBe('false');
  });
});

describe('selected event panel', () => {
  it('pins the event header and keeps the scrolling body keyboard focusable', () => {
    const values = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value); },
      removeItem: (key: string) => { values.delete(key); },
    });
    const event = { eventId: 7, eventTime: '2026-10-24T17:00:01Z', type: 'StepExecuteCompleted' as const, payload: {} };
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowStatePanel selectedEvent={event} history={[event]} parentFlowId="order-1" />
        </PreferencesProvider>
      </MemoryRouter>,
    );
    const panel = screen.getByRole('region', { name: 'Selected event details' });
    expect(panel.getAttribute('tabindex')).toBe('0');
    expect(panel.querySelector('.selected-event-anchor')?.textContent).toContain('#7');
    expect(panel.querySelector('.selected-event-anchor')?.textContent).not.toContain('2026-10-24T17:00:01Z');
  });
});

describe('selected event panel header', () => {
  it('shows the event offset from the run start', () => {
    const values = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value); },
      removeItem: (key: string) => { values.delete(key); },
    });
    const event = { eventId: 9, eventTime: '2026-10-24T17:02:05Z', type: 'StepExecuteCompleted' as const, payload: {} };
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowStatePanel selectedEvent={event} history={[event]} parentFlowId="order-1" runStartTime="2026-10-24T17:00:00Z" />
        </PreferencesProvider>
      </MemoryRouter>,
    );
    const header = screen.getByRole('region', { name: 'Selected event details' }).querySelector('.selected-event-anchor');
    expect(header?.textContent).toContain('+2m 5s');
  });
});

