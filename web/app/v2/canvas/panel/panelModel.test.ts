// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { PocFlow, StepModel } from '../model/pocFlow';
import type { RunOverlay, StepExecution } from '../model/run';
import { buildPanel } from './panelModel';

function step(): StepModel {
  return {
    id: 'step:RefundStep',
    stepType: 'RefundStep',
    label: 'Refund',
    isStart: true,
    actor: 'machine',
    isHub: false,
    isRecoveryHub: false,
    recoveryRole: 'none',
    hasUniformFailurePolicy: false,
    inboundCount: 0,
    inboundFailureCount: 0,
    waitFor: null,
    execute: { branches: [{ decisionTypes: ['goTo'], guard: null, fullGuards: [], targets: [] }] },
    resources: [],
  };
}

function flow(model: StepModel): PocFlow {
  return {
    flowType: 'RefundFlow',
    valid: true,
    source: { language: 'go', path: 'refund.go' },
    steps: [model],
    entries: [],
    resources: [],
    subflows: [],
    transitions: [],
    diagnostics: [],
    provenance: { generated: true, note: 'test' },
    dropped: [],
  };
}

function overlay(execution: StepExecution): RunOverlay {
  return {
    runId: 'run-2',
    flowId: 'flow-1',
    status: 'Running',
    executions: [execution],
    simulated: false,
    note: 'run-2',
    now: 1,
  };
}

describe('buildPanel', () => {
  it('defaults to Input when a run payload is present', () => {
    const model = step();
    const panel = buildPanel(
      flow(model),
      model,
      overlay({
        stepExecutionId: 'exec-2',
        stepType: 'RefundStep',
        ordinal: 1,
        waitFor: null,
        execute: { status: 'completed' },
        attempts: 1,
      }),
      null,
      { input: { stepInput: { amount: 12 } }, output: { stepDecision: { type: 'goTo' } }, context: { stepType: 'RefundStep' } },
    );
    expect(panel.defaultSection).toBe('input');
    expect(panel.sections.map((section) => section.id)).toContain('input');
    expect(panel.sections.map((section) => section.id)).toContain('output');
    expect(panel.sections.map((section) => section.id)).toContain('context');
    expect(panel.sections.find((section) => section.id === 'executions')).toBeUndefined();
  });
});
