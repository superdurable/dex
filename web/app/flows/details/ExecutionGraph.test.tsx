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
import { StepNodeLabel, graphNodeDimensions, newestFoldedViewportX } from './ExecutionGraph';
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

function waitingOnChannels(names: string[]): StepGraphNode {
  return {
    ...completedNode,
    waitFor: {
      eventId: 2,
      eventTime: null,
      type: 'StepWaitForCompleted',
      payload: {
        output: {
          waitForCondition: {
            timerConditions: [{}],
            channelConditions: names.map((channelName) => ({ channelName })),
          },
        },
      },
    },
  };
}

describe('Execution graph WaitFor Channel lines', () => {
  const twoChannelHeight = graphNodeDimensions(waitingOnChannels(['A', 'B'])).height;
  const channelLineHeight = 14;

  it.each([
    { count: 3, hidden: 1 },
    { count: 6, hidden: 4 },
  ])('shows two of $count Channel names and a "+$hidden more" line, with the full list as a tooltip', ({ count, hidden }) => {
    const names = Array.from({ length: count }, (_, index) => `Channel${index + 1}`);
    render(<StepNodeLabel node={waitingOnChannels(names)} onSelect={vi.fn()} />);
    const lines = screen.getByText('Channel1').parentElement;
    expect([...(lines?.children ?? [])].map((child) => child.textContent)).toEqual(['Channel1', 'Channel2', `+${hidden} more`]);
    expect(lines?.closest('i')?.getAttribute('title')).toBe(names.join(', '));
  });

  it.each([3, 6])('sizes the node for the visible lines of %i Channel names', (count) => {
    const names = Array.from({ length: count }, (_, index) => `Channel${index + 1}`);
    // WaitFor base, one timer line, then two more Channel lines: two names and "+N more".
    expect(graphNodeDimensions(waitingOnChannels(names)).height).toBe(148 + 18 + 2 * channelLineHeight);
    expect(graphNodeDimensions(waitingOnChannels(names)).height).toBe(twoChannelHeight + channelLineHeight);
  });

  it('adds one Channel line per visible name up to two', () => {
    expect(twoChannelHeight - graphNodeDimensions(waitingOnChannels(['A'])).height).toBe(channelLineHeight);
  });
});

describe('Folded Graph newest-part framing', () => {
  const canvasWidth = 1091;
  const zoom = 0.8;
  // Flow start and Earlier events share a column; then StartAgentSession, RunModel, Round 1, and the Round 2 frame.
  const units = [
    { left: 32, right: 252 },
    { left: 32, right: 200 },
    { left: 316, right: 616 },
    { left: 680, right: 980 },
    { left: 1044, right: 1324 },
    { left: 1374, right: 1700 },
  ];
  const newest = { x: 1044, width: 656, units };

  it('starts at a node boundary, so no node is cut at the left edge', () => {
    const x = newestFoldedViewportX(newest, zoom, canvasWidth);
    for (const unit of units) {
      const left = unit.left * zoom + x;
      const right = unit.right * zoom + x;
      const isWhollyShown = left >= 0 && right <= canvasWidth;
      const isWhollyHidden = right <= 0;
      expect(isWhollyShown || isWhollyHidden).toBe(true);
    }
    // The newest part's right edge stays in view, and RunModel, which fits, is shown whole.
    expect(1700 * zoom + x).toBeLessThanOrEqual(canvasWidth);
    expect(680 * zoom + x).toBe(24);
  });

  it('aligns right when the newest part alone is wider than the canvas', () => {
    const wide = { x: 100, width: 2000, units: [{ left: 100, right: 2100 }] };
    expect(newestFoldedViewportX(wide, zoom, canvasWidth)).toBe(canvasWidth - 24 - 2100 * zoom);
  });
});

function nodeWithDecision(stepDecision: unknown): StepGraphNode {
  return {
    ...completedNode,
    execute: { eventId: 3, eventTime: null, type: 'StepExecuteCompleted', payload: { output: { stepDecision } } },
  };
}

describe('Execution graph Execute decision', () => {
  it.each([
    [{ nextSteps: [{ stepType: 'SyncBatch' }] }, 'go-to', 'GoTo', 'lucide-arrow-right'],
    [{ closeDecision: { closeDecisionType: 5 } }, 'dead-end', 'DeadEnd', 'lucide-circle-slash-2'],
    [{ closeDecision: { closeDecisionType: 2 } }, 'graceful-complete', 'GracefulComplete', 'lucide-flag'],
    [{ closeDecision: { closeDecisionType: 3 } }, 'force-complete', 'ForceComplete', 'lucide-check-check'],
    [{ closeDecision: { closeDecisionType: 4 } }, 'force-fail', 'ForceFail', 'lucide-octagon-x'],
    [{ closeDecision: { closeDecisionType: 1 } }, 'force-complete-if-channels-empty', 'ForceCompleteIfChannelsEmpty', 'lucide-inbox'],
    [{ cancelStepTypes: ['Poll'] }, 'cancel-steps', 'CancelSteps', 'lucide-ban'],
  ])('shows the decision %j with its SDK name, icon, and tone', (stepDecision, kind, label, iconClass) => {
    render(<StepNodeLabel node={nodeWithDecision(stepDecision)} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: new RegExp(`^Execute ${label}`) });
    expect(execute.className).toContain(`graph-method-decision-${kind}`);
    expect(execute.querySelector('.graph-execute-decision-text')?.textContent).toBe(label);
    expect(execute.querySelector(`svg.${iconClass}`)).not.toBeNull();
    expect(execute.textContent).not.toContain('Completed');
    const visibleText = execute.querySelector('.graph-execute-decision-lines')?.textContent;
    expect(visibleText).not.toContain('SyncBatch');
    expect(visibleText).not.toContain('Poll');
    expect(execute.querySelector('.graph-execute-decision-channels')).toBeNull();
  });

  it('shows GoTo without a count for one next Step and GoTo ×N for several', () => {
    render(<StepNodeLabel node={nodeWithDecision({ nextSteps: [{ stepType: 'Notify' }] })} onSelect={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Execute GoTo' }).querySelector('.graph-execute-decision-count')).toBeNull();
    cleanup();
    render(<StepNodeLabel node={nodeWithDecision({
      nextSteps: [{ stepType: 'ResumeTurn' }, { stepType: 'CallTool' }, { stepType: 'Audit' }],
    })} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute GoTo ×3' });
    expect(execute.querySelector('.graph-execute-decision-count')?.textContent).toBe('×3');
    expect(execute.textContent).not.toContain('GoToMany');
    expect(execute.querySelector('.graph-execute-decision-lines')?.getAttribute('title'))
      .toBe('GoTo → ResumeTurn\nGoTo → CallTool\nGoTo → Audit');
  });

  it('adds CancelSteps ×N to the decision kind and lists the cancellation in the tooltip and description', () => {
    render(<StepNodeLabel node={nodeWithDecision({
      nextSteps: [{ stepType: 'RequestHumanApprovalStep' }],
      cancelStepTypes: ['GetUsageStep'],
    })} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute GoTo CancelSteps ×1' });
    expect(execute.querySelector('.graph-execute-decision-separator')?.textContent).toBe('·');
    expect(execute.className).toContain('graph-method-decision-go-to');
    const cancel = execute.querySelector('.graph-execute-decision-cancel')!;
    expect(cancel.querySelector('svg.lucide-ban')).not.toBeNull();
    expect(cancel.textContent).toBe('CancelSteps×1');
    const fullText = 'GoTo → RequestHumanApprovalStep\nCancelSteps → GetUsageStep (entire Flow)';
    expect(execute.querySelector('.graph-execute-decision-lines')?.getAttribute('title')).toBe(fullText);
    expect(screen.getByRole('button', { description: fullText.split('\n').join('; ') })).toBe(execute);
  });

  it('shows CancelSteps ×N alone for a decision that only cancels', () => {
    render(<StepNodeLabel node={nodeWithDecision({ cancelStepTypes: ['Poll'], cancelSiblingStepTypes: ['Retry'] })} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute CancelSteps ×2' });
    expect(execute.className).toContain('graph-method-decision-cancel-steps');
    expect(execute.querySelector('.graph-execute-decision-cancel')).toBeNull();
    expect(screen.getByRole('button', {
      description: 'CancelSteps → Poll (entire Flow); CancelSiblingSteps → Retry (same parent execution)',
    })).toBe(execute);
  });

  it('describes the whole decision to the focused Execute button', () => {
    render(<StepNodeLabel node={nodeWithDecision({
      nextSteps: [{ stepType: 'ResumeTurn' }, { stepType: 'CallTool' }],
      cancelStepTypes: ['Poll'],
      closeDecision: { closeDecisionType: 2 },
    })} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: /^Execute/ });
    execute.focus();
    expect(document.activeElement).toBe(execute);
    const description = 'GracefulComplete; GoTo → ResumeTurn; GoTo → CallTool; CancelSteps → Poll (entire Flow)';
    expect(document.getElementById(execute.getAttribute('aria-describedby') ?? '')?.textContent).toBe(description);
    expect(screen.getByRole('button', { description })).toBe(execute);
    expect(screen.getByRole('button', { name: 'Execute GracefulComplete CancelSteps ×1' })).toBe(execute);
  });

  it('keeps the ForceFail reason in the tooltip and description', () => {
    render(<StepNodeLabel node={nodeWithDecision({
      closeDecision: { closeDecisionType: 4, closeInput: { stringValue: 'card declined' } },
    })} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute ForceFail' });
    expect(execute.querySelector('.graph-execute-decision-lines')?.getAttribute('title')).toBe('ForceFail: card declined');
    expect(screen.getByRole('button', { description: 'ForceFail: card declined' })).toBe(execute);
  });

  it.each([
    [['orders'], 'orders', null],
    [['orders', 'refunds'], 'orders, refunds', null],
    [['orders', 'refunds', 'audits', 'payouts'], 'orders, refunds', '+2'],
  ])('shows up to two of the Channels %j under ForceCompleteIfChannelsEmpty', (channels, shown, more) => {
    render(<StepNodeLabel node={nodeWithDecision({
      closeDecision: { closeDecisionType: 1, conditionalChannelNames: channels },
    })} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: /^Execute ForceCompleteIfChannelsEmpty/ });
    const channelLine = execute.querySelector('.graph-execute-decision-channels')!;
    expect(channelLine.querySelector('svg.graph-condition-icon')).not.toBeNull();
    expect(channelLine.querySelector('.graph-execute-decision-channel-names')?.textContent).toBe(shown);
    expect(channelLine.querySelector('.graph-execute-decision-count')?.textContent ?? null).toBe(more);
    const fullText = `ForceCompleteIfChannelsEmpty → channels ${channels.join(', ')}`;
    expect(execute.querySelector('.graph-execute-decision-lines')?.getAttribute('title')).toBe(fullText);
    expect(screen.getByRole('button', { description: fullText })).toBe(execute);
  });

  it('grows only an Execute-only node with a Channel line', () => {
    const baseHeight = graphNodeDimensions(completedNode).height;
    for (const stepDecision of [
      { nextSteps: [{ stepType: 'A' }, { stepType: 'B' }], cancelStepTypes: ['C'] },
      { closeDecision: { closeDecisionType: 4 } },
      { closeDecision: { closeDecisionType: 1 } },
    ]) {
      expect(graphNodeDimensions(nodeWithDecision(stepDecision)).height).toBe(baseHeight);
    }
    const withChannels = nodeWithDecision({ closeDecision: { closeDecisionType: 1, conditionalChannelNames: ['orders'] } });
    expect(graphNodeDimensions(withChannels).height).toBeGreaterThan(baseHeight);
    const withWaitFor: StepGraphNode = {
      ...withChannels,
      waitFor: { eventId: 2, eventTime: null, type: 'StepWaitForCompleted', payload: {} },
    };
    expect(graphNodeDimensions(withWaitFor).height).toBe(graphNodeDimensions({ ...completedNode, waitFor: withWaitFor.waitFor }).height);
  });

  it('keeps the current status of a Step with WaitFor only and no Execute event yet', () => {
    const node: StepGraphNode = {
      id: 'AwaitApproval-1',
      label: 'AwaitApproval',
      kind: 'step',
      status: 'Waiting',
      waitFor: { eventId: 2, eventTime: null, type: 'StepWaitForCompleted', payload: {} },
      active: { phase: 'Waiting' } as StepGraphNode['active'],
    };
    render(<StepNodeLabel node={node} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute Not started' });
    expect(execute.getAttribute('aria-describedby')).toBeNull();
    expect(execute.querySelector('.graph-execute-decision')).toBeNull();
  });

  it('keeps the failure status for a failed Execute', () => {
    const node: StepGraphNode = {
      ...completedNode,
      status: 'Failed',
      execute: {
        eventId: 3,
        eventTime: null,
        type: 'StepExecuteFailed',
        payload: { output: { stepDecision: { nextSteps: [{ stepType: 'Recover' }] } } },
      },
    };
    render(<StepNodeLabel node={node} onSelect={vi.fn()} />);
    const execute = screen.getByRole('button', { name: 'Execute Failed' });
    expect(execute.className).toContain('graph-method-failed');
    expect(execute.querySelector('.graph-execute-decision')).toBeNull();
  });

  it('keeps Completed when the decision has nothing to show', () => {
    render(<StepNodeLabel node={nodeWithDecision({ closeDecision: { closeDecisionType: 0 } })} onSelect={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Execute Completed' })).toBeTruthy();
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

