// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { PocFlow, StepModel } from '../canvas/model/pocFlow';
import type { StepGroup } from '../canvas/views/groups';
import { stepContext } from './stepContext';

const step = (over: Partial<StepModel> & { id: string; stepType: string }): StepModel => ({
  label: over.stepType,
  isStart: false,
  actor: 'machine',
  isHub: false,
  isRecoveryHub: false,
  recoveryRole: 'none',
  hasUniformFailurePolicy: false,
  inboundCount: 0,
  inboundFailureCount: 0,
  isConnectorStep: false,
  waitFor: null,
  execute: { branches: [], kind: 'runs' } as StepModel['execute'],
  resources: [],
  ...over,
});

const transition = (from: string, to: string, kind: 'transition' | 'failure_transition') => ({
  id: `${from}->${to}`,
  fromStepId: from,
  toStepId: to,
  kind,
  guard: null,
  mergedGuards: [],
  isSelfLoop: false,
});

const gate = step({
  id: 'step:Gate',
  stepType: 'GateStep',
  actor: 'external',
  explanation: 'Pause for a manager decision before issuing a refund.',
  waitFor: { type: 'until', conditions: [], sentence: 'waits for a message on manager-approval' },
});
const guard = step({ id: 'step:Guard', stepType: 'GuardrailStep' });
const next = step({ id: 'step:Next', stepType: 'ReCheckStep' });
const sink = step({ id: 'step:Sink', stepType: 'BillingFailedStep' });

const flow = {
  steps: [guard, gate, next, sink],
  transitions: [
    transition('step:Guard', 'step:Gate', 'transition'),
    transition('step:Gate', 'step:Next', 'transition'),
    transition('step:Gate', 'step:Sink', 'failure_transition'),
  ],
} as unknown as PocFlow;

const groups: StepGroup[] = [
  { id: 'control', label: 'Control', reason: 'Control', stepTypes: ['GuardrailStep', 'GateStep'] } as StepGroup,
];

describe('stepContext', () => {
  it('leads with the purpose the Flow declared', () => {
    expect(stepContext(flow, gate, groups).explanation)
      .toBe('Pause for a manager decision before issuing a refund.');
  });

  it('never invents a purpose the Flow did not declare', () => {
    expect(stepContext(flow, guard, groups).explanation).toBeNull();
  });

  it('places the Step in its phase and names who acts', () => {
    const facts = labelled(stepContext(flow, gate, groups));
    expect(facts['In phase']).toBe('Control');
    expect(facts['Who acts']).toBe('a person or system outside the flow');
  });

  it('reads what reaches it and where it goes from the graph', () => {
    const facts = labelled(stepContext(flow, gate, groups));
    expect(facts['Reached from']).toBe('GuardrailStep');
    expect(facts['Then goes to']).toBe('ReCheckStep');
  });

  it('keeps recovery separate from the ordinary path', () => {
    const facts = labelled(stepContext(flow, gate, groups));
    expect(facts['If it fails']).toBe('BillingFailedStep');
    expect(facts['Then goes to']).not.toContain('BillingFailedStep');
  });

  it('reuses the wait phase\'s own prose rather than rebuilding it', () => {
    expect(labelled(stepContext(flow, gate, groups))['Waits for'])
      .toBe('waits for a message on manager-approval');
  });

  it('omits a wait line for a Step that does not wait', () => {
    expect(labelled(stepContext(flow, next, groups))['Waits for']).toBeUndefined();
  });

  it('says the flow starts here rather than showing an empty inbound list', () => {
    const start = step({ id: 'step:Start', stepType: 'ReceiveStep', isStart: true });
    const withStart = { ...flow, steps: [...flow.steps, start] } as unknown as PocFlow;
    expect(labelled(stepContext(withStart, start, groups))['Reached from'])
      .toBe('the flow starts here');
  });

  it('omits the phase line when no group claims the Step', () => {
    expect(labelled(stepContext(flow, sink, groups))['In phase']).toBeUndefined();
  });
});

function labelled(view: ReturnType<typeof stepContext>): Record<string, string> {
  return Object.fromEntries(view.facts.map((fact) => [fact.label, fact.value]));
}
