// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest'
import type { FlowHistoryEvent } from '@/lib/types'
import type { PocFlow, StepModel } from '../model/pocFlow'
import type { RunOverlay, StepExecution } from '../model/run'
import { buildPanel } from './panelModel'

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
      },
      {
        ...model,
        id: 'step:AgentDecisionStep',
        stepType: 'AgentDecisionStep',
        label: 'AgentDecision',
        isStart: false,
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

function overlay(execution: StepExecution): RunOverlay {
  return {
    runId: 'run-2',
    flowId: 'flow-1',
    status: 'Running',
    executions: [execution],
    simulated: false,
    note: 'run-2',
    now: 1,
  }
}

function methodEvent(): FlowHistoryEvent {
  return {
    eventId: 12,
    eventTime: '2026-09-18T00:00:00.000Z',
    type: 'StepExecuteCompleted',
    payload: {
      input: { stepInput: { amount: 12 } },
      output: { stepDecision: { type: 'goTo', nextSteps: [{ stepType: 'NotifyStep' }] } },
      context: { stepType: 'RefundStep', stepExecutionId: 'exec-2', finalAttempt: 1 },
    },
  }
}

describe('buildPanel', () => {
  it('defaults to Input when a method event is present', () => {
    const model = step()
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
        decisionType: 'goTo',
        nextStepTypes: ['NotifyStep'],
      }),
      null,
      methodEvent(),
      1,
      true,
    )
    expect(panel.defaultSection).toBe('input')
    expect(panel.sections.map((section) => section.id)).toContain('input')
    expect(panel.sections.map((section) => section.id)).toContain('output')
    expect(panel.sections.map((section) => section.id)).toContain('context')
    expect(panel.sections.find((section) => section.id === 'executions')).toBeUndefined()
    const execute = panel.sections.find((section) => section.id === 'execute')
    expect(execute?.body.kind).toBe('execute')
    if (execute?.body.kind === 'execute') {
      expect(execute.body.nextStepTypes).toEqual(['NotifyStep'])
      expect(execute.body.hasExecuteEvent).toBe(true)
      expect(execute.body.attempts).toHaveLength(1)
    }
  })

  it('omits Input tabs and reports missing Execute without a method event', () => {
    const model = step()
    const panel = buildPanel(
      flow(model),
      model,
      overlay({
        stepExecutionId: 'exec-2',
        stepType: 'RefundStep',
        ordinal: 1,
        waitFor: null,
        execute: { status: 'notStarted' },
        attempts: 1,
      }),
      null,
      null,
      0,
      false,
    )
    expect(panel.defaultSection).toBe('overview')
    expect(panel.sections.map((section) => section.id)).not.toContain('input')
    const execute = panel.sections.find((section) => section.id === 'execute')
    expect(execute?.body.kind).toBe('execute')
    if (execute?.body.kind === 'execute') {
      expect(execute.body.hasExecuteEvent).toBe(false)
      expect(execute.body.attempts).toEqual([])
      expect(execute.body.nextStepTypes).toEqual([])
    }
  })
})
