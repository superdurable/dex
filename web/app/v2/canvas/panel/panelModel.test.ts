// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest'
import type { PocFlow, StepModel } from '../model/pocFlow'
import type { RunOverlay, StepExecution } from '../model/run'
import { buildPanel } from './panelModel'

function step(wait = false): StepModel {
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
    waitFor: wait
      ? {
          type: 'anyOf',
          sentence: 'approval or timeout',
          conditions: [
            { kind: 'channel', label: 'approvals' },
            { kind: 'timer', label: '24h' },
          ],
        }
      : null,
    execute: {
      branches: [
        {
          decisionTypes: ['goTo'],
          guard: null,
          fullGuards: [],
          targets: [
            { stepId: 'step:NotARefundStep' },
            { stepId: 'step:AgentDecisionStep' },
          ],
        },
      ],
    },
    resources: [],
    explanation: 'Decide the next refund capability from gathered evidence.',
  }
}

function flow(model: StepModel): PocFlow {
  return {
    flowType: 'RefundFlow',
    valid: true,
    source: { language: 'go', path: 'refund.go' },
    steps: [
      model,
      {
        ...model,
        id: 'step:NotARefundStep',
        stepType: 'NotARefundStep',
        label: 'NotARefund',
        isStart: false,
        waitFor: null,
      },
      {
        ...model,
        id: 'step:AgentDecisionStep',
        stepType: 'AgentDecisionStep',
        label: 'AgentDecision',
        isStart: false,
        waitFor: null,
      },
    ],
    entries: [],
    resources: [],
    subflows: [],
    transitions: [],
    diagnostics: [],
    provenance: { generated: true, note: 'test' },
    dropped: [],
  }
}

function overlay(executions: StepExecution[]): RunOverlay {
  return {
    runId: 'run-2',
    flowId: 'flow-1',
    status: 'Running',
    executions,
    simulated: false,
    note: 'run-2',
    now: 1,
  }
}

describe('buildPanel', () => {
  it('splits Execution into WaitFor and Execute phases with separate IO tabs', () => {
    const model = step(true)
    const panel = buildPanel(
      flow(model),
      model,
      overlay([
        {
          stepExecutionId: 'exec-1',
          stepType: 'RefundStep',
          ordinal: 1,
          waitFor: {
            status: 'completed',
            conditions: [
              { kind: 'channel', label: 'approvals', satisfied: true },
              { kind: 'timer', label: '24h', satisfied: false },
            ],
          },
          execute: { status: 'completed' },
          attempts: 1,
          decisionType: 'goTo',
          nextStepTypes: ['NotifyStep'],
        },
        {
          stepExecutionId: 'exec-2',
          stepType: 'RefundStep',
          ordinal: 2,
          waitFor: {
            status: 'completed',
            conditions: [
              { kind: 'channel', label: 'approvals', satisfied: true },
              { kind: 'timer', label: '24h', satisfied: false },
            ],
          },
          execute: { status: 'completed' },
          attempts: 1,
          decisionType: 'goTo',
          nextStepTypes: ['NotifyStep'],
        },
      ]),
      'exec-2',
      true,
      true,
      1,
    )
    expect(panel.definition.branches.map((branch) => branch.value)).toEqual([
      'unconditional → NotARefund, AgentDecision',
    ])
    expect(panel.executions.map((execution) => execution.id)).toEqual(['exec-1', 'exec-2'])
    expect(panel.phases.map((phase) => phase.id)).toEqual(['wait', 'execute'])
    expect(panel.phases[0].tabs.map((tab) => tab.id)).toEqual(['input', 'context', 'output'])
    expect(panel.phases[1].tabs.map((tab) => tab.id)).toEqual(['input', 'context', 'output'])
    expect(panel.defaultSection).toBe('execute-input')
    const waitOutput = panel.phases[0].tabs.find((tab) => tab.id === 'output')
    expect(waitOutput?.body.kind).toBe('waitOutput')
    if (waitOutput?.body.kind === 'waitOutput') {
      expect(waitOutput.body.winner).toBe('approvals')
      expect(waitOutput.body.hasWaitEvent).toBe(true)
    }
    const executeOutput = panel.phases[1].tabs.find((tab) => tab.id === 'output')
    expect(executeOutput?.body.kind).toBe('executeOutput')
    if (executeOutput?.body.kind === 'executeOutput') {
      expect(executeOutput.body.nextStepTypes).toEqual(['NotifyStep'])
      expect(executeOutput.body.hasExecuteEvent).toBe(true)
    }
  })

  it('keeps Execute-only Steps without a WaitFor phase', () => {
    const model = step(false)
    const panel = buildPanel(
      flow(model),
      model,
      overlay([
        {
          stepExecutionId: 'exec-2',
          stepType: 'RefundStep',
          ordinal: 1,
          waitFor: null,
          execute: { status: 'notStarted' },
          attempts: 1,
        },
      ]),
      null,
      false,
      false,
      0,
    )
    expect(panel.phases.map((phase) => phase.id)).toEqual(['execute'])
    expect(panel.defaultSection).toBe('execute-output')
    const executeOutput = panel.phases[0].tabs.find((tab) => tab.id === 'output')
    expect(executeOutput?.body.kind).toBe('executeOutput')
    if (executeOutput?.body.kind === 'executeOutput') {
      expect(executeOutput.body.hasExecuteEvent).toBe(false)
      expect(executeOutput.body.attempts).toEqual([])
      expect(executeOutput.body.nextStepTypes).toEqual([])
    }
  })
})
