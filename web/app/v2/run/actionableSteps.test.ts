// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { PocFlow } from '../canvas/model/pocFlow';
import type { RunOverlay } from '../canvas/model/run';
import { actionableSteps, hasMultipleActions } from './actionableSteps';

/**
 * A two-gate refund shape: start -> guard -> approve(gate) -> draft -> confirm(gate) -> send.
 * The gates wait on their own Channels, each opened by its own external entry.
 */
const flow = {
  steps: [
    step('step:Start', 'ReceiveStep', { isStart: true }),
    step('step:Guard', 'GuardrailStep'),
    step('step:Approve', 'RequestHumanApprovalStep', { channel: 'manager-approval' }),
    step('step:Draft', 'DraftCustomerMessageStep'),
    step('step:Confirm', 'ConfirmCustomerMessageStep', { channel: 'message-approval' }),
    step('step:Send', 'SendCustomerMessageStep'),
  ],
  transitions: [
    edge('step:Start', 'step:Guard'),
    edge('step:Guard', 'step:Approve'),
    edge('step:Approve', 'step:Draft'),
    edge('step:Draft', 'step:Confirm'),
    edge('step:Confirm', 'step:Send'),
  ],
  entries: [
    { name: 'ApproveRefund', opensGates: [{ channelId: 'manager-approval' }] },
    { name: 'RejectRefund', opensGates: [{ channelId: 'manager-approval' }] },
    { name: 'ConfirmCustomerMessage', opensGates: [{ channelId: 'message-approval' }] },
  ],
} as unknown as PocFlow;

function step(id: string, stepType: string, opts: { isStart?: boolean; channel?: string } = {}) {
  return {
    id,
    stepType,
    label: stepType,
    isStart: opts.isStart ?? false,
    actor: 'machine',
    waitFor: opts.channel === undefined ? null : {
      type: 'until',
      sentence: `waits on ${opts.channel}`,
      conditions: [{ kind: 'channel', resourceId: opts.channel, label: opts.channel }],
    },
    execute: { branches: [] },
    resources: [],
  };
}

function edge(fromStepId: string, toStepId: string) {
  return { id: `${fromStepId}->${toStepId}`, fromStepId, toStepId, kind: 'transition', guard: null, mergedGuards: [], isSelfLoop: false };
}

const overlay = (open: string[], ran: string[]) => ({
  executions: [
    ...ran.map((stepType) => ({
      stepType, waitFor: { status: 'completed' }, execute: { status: 'completed' },
    })),
    ...open.map((stepType) => ({
      stepType, waitFor: { status: 'waiting' }, execute: { status: 'not_started' },
    })),
  ],
} as unknown as RunOverlay);

describe('actionableSteps', () => {
  it('finds only the Steps something outside the Flow answers', () => {
    expect(actionableSteps(flow, null).map((s) => s.stepType))
      .toEqual(['RequestHumanApprovalStep', 'ConfirmCustomerMessageStep']);
  });

  it('orders them the way the graph reaches them, not by name', () => {
    const reordered = { ...flow, steps: [...flow.steps].reverse() } as unknown as PocFlow;
    expect(actionableSteps(reordered, null).map((s) => s.stepType))
      .toEqual(['RequestHumanApprovalStep', 'ConfirmCustomerMessageStep']);
  });

  it('names the RPCs that answer each gate', () => {
    const [approve, confirm] = actionableSteps(flow, null);
    expect(approve.answeredBy).toEqual(['ApproveRefund', 'RejectRefund']);
    expect(confirm.answeredBy).toEqual(['ConfirmCustomerMessage']);
  });

  it('marks the open gate current and the later one upcoming', () => {
    const steps = actionableSteps(flow, overlay(['RequestHumanApprovalStep'], ['GuardrailStep']));
    expect(steps.map((s) => s.state)).toEqual(['current', 'upcoming']);
  });

  it('advances: the answered gate is done and the next one is current', () => {
    const steps = actionableSteps(
      flow,
      overlay(['ConfirmCustomerMessageStep'], ['GuardrailStep', 'RequestHumanApprovalStep']),
    );
    expect(steps.map((s) => s.state)).toEqual(['done', 'current']);
  });

  it('shows every gate done once the run has closed', () => {
    const steps = actionableSteps(
      flow,
      overlay([], ['RequestHumanApprovalStep', 'ConfirmCustomerMessageStep']),
    );
    expect(steps.map((s) => s.state)).toEqual(['done', 'done']);
  });

  it('calls a gate a branch skipped done rather than still pending', () => {
    // Small refunds never reach the manager gate, but still need the message confirmed.
    const steps = actionableSteps(flow, overlay(['ConfirmCustomerMessageStep'], ['GuardrailStep']));
    expect(steps.map((s) => s.state)).toEqual(['done', 'current']);
  });

  it('treats everything as upcoming before a run exists', () => {
    expect(actionableSteps(flow, null).map((s) => s.state)).toEqual(['upcoming', 'upcoming']);
  });

  it('draws no stepper for a single gate', () => {
    expect(hasMultipleActions(actionableSteps(flow, null))).toBe(true);
    expect(hasMultipleActions([])).toBe(false);
    expect(hasMultipleActions(actionableSteps(flow, null).slice(0, 1))).toBe(false);
  });
});
