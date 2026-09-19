// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { PocFlow, StepModel } from '../model/pocFlow'
import type { PhaseStatus, RunOverlay } from '../model/run'
import {
  executionsOf,
  PHASE_LABEL,
  waitKind,
} from '../model/run'
import { answeredBy } from '../views/stepBox'

export type PhaseId = 'wait' | 'execute'
export type PhaseTabId = 'input' | 'context' | 'output'
export type SectionId = `${PhaseId}-${PhaseTabId}`

export interface Row {
  label: string
  value: string
  tone?: 'normal' | 'quiet' | 'warn' | 'good'
  /** Raw text that must not be truncated — full guards, full error messages. */
  pre?: boolean
}

export type Verdict = 'won' | 'lostStillQueued' | 'notEvaluated' | 'pending'

export const VERDICT_TEXT: Record<Verdict, string> = {
  won: 'this one arrived',
  lostStillQueued: 'lost the race, still queued',
  notEvaluated: 'never evaluated',
  pending: 'still waiting',
}

export interface WaitRow {
  kind: string
  label: string
  verdict: Verdict
}

export interface AttemptRow {
  n: number
  status: PhaseStatus
  failure?: string
}

export interface ExecutionChoice {
  id: string
  wait: string
  execute: string
  won: string
}

export interface DefinitionModel {
  explanation: string | null
  waitFor: string | null
  waitConditions: string[]
  branches: Row[]
}

export type TabBody =
  | { kind: 'stepMethod'; part: 'input' | 'context' }
  | {
      kind: 'waitOutput'
      rows: WaitRow[]
      winner: string | null
      answeredBy: string[]
      hasWaitEvent: boolean
    }
  | {
      kind: 'executeOutput'
      attempts: AttemptRow[]
      attemptsLeft: string
      decision: string | null
      nextStepTypes: string[]
      hasExecuteEvent: boolean
      error?: string
    }

export interface PhaseTab {
  id: PhaseTabId
  label: string
  body: TabBody
}

export interface ExecutionPhase {
  id: PhaseId
  label: string
  defaultTab: PhaseTabId
  tabs: PhaseTab[]
}

export interface PanelModel {
  title: string
  stepType: string
  defaultSection: SectionId
  definition: DefinitionModel
  executions: ExecutionChoice[]
  phases: ExecutionPhase[]
}

function statusWord(status: PhaseStatus): string {
  return PHASE_LABEL[status]
}

function sectionId(phase: PhaseId, tab: PhaseTabId): SectionId {
  return `${phase}-${tab}`
}

export function parseSectionId(id: SectionId): { phase: PhaseId; tab: PhaseTabId } {
  const [phase, tab] = id.split('-') as [PhaseId, PhaseTabId]
  return { phase, tab }
}

export function buildPanel(
  flow: PocFlow,
  step: StepModel,
  overlay: RunOverlay | null,
  selectedExecutionId: string | null,
  hasWaitEvent = false,
  hasExecuteEvent = false,
  attemptCount = 0,
): PanelModel {
  const execs = overlay === null ? [] : executionsOf(overlay, step.stepType)
  const current =
    execs.find((execution) => execution.stepExecutionId === selectedExecutionId)
    ?? execs[execs.length - 1]
    ?? null

  const definition: DefinitionModel = {
    explanation: step.explanation ?? null,
    waitFor: step.waitFor === null ? null : `${step.waitFor.type} · ${step.waitFor.sentence}`,
    waitConditions: step.waitFor?.conditions.map((condition) => `${condition.label} (${condition.kind})`) ?? [],
    branches: step.execute.branches.map((branch) => {
      const targets = branch.targets
        .map((target) => flow.steps.find((candidate) => candidate.id === target.stepId)?.label
          ?? target.stepId.replace(/^step:/, ''))
        .join(', ')
      return {
        label: branch.decisionTypes.join('/') || 'goTo',
        value: [
          branch.fullGuards.length > 0 ? branch.fullGuards.join('  ·  ') : 'unconditional',
          targets.length > 0 ? `→ ${targets}` : '→ closes',
        ].join(' '),
        pre: branch.fullGuards.length > 0,
      }
    }),
  }

  const phases: ExecutionPhase[] = []

  if (step.waitFor !== null) {
    const declared = step.waitFor.conditions
    const live = current?.waitFor?.conditions ?? []
    const blocked = current?.waitFor?.status === 'waiting' || current?.waitFor?.status === 'pending'
    const anyWon = live.some((condition) => condition.satisfied)
    const rows: WaitRow[] = declared.map((declaredCondition) => {
      const liveRow = live.find((condition) => condition.label === declaredCondition.label)
      let verdict: Verdict = 'notEvaluated'
      if (liveRow !== undefined) {
        if (liveRow.satisfied) verdict = 'won'
        else if (blocked) verdict = 'pending'
        else if (anyWon) verdict = 'lostStillQueued'
      }
      return { kind: declaredCondition.kind, label: declaredCondition.label, verdict }
    })
    if (declared.length === 0) {
      rows.push({ kind: 'unknown', label: 'not reported by the analyser', verdict: 'notEvaluated' })
    }
    const waitDefault: PhaseTabId = hasWaitEvent ? 'input' : 'output'
    phases.push({
      id: 'wait',
      label: `WaitFor · ${step.waitFor.type}`,
      defaultTab: waitDefault,
      tabs: [
        { id: 'input', label: 'Input', body: { kind: 'stepMethod', part: 'input' } },
        { id: 'context', label: 'Context', body: { kind: 'stepMethod', part: 'context' } },
        {
          id: 'output',
          label: 'Output',
          body: {
            kind: 'waitOutput',
            rows,
            winner: live.find((condition) => condition.satisfied)?.label ?? null,
            answeredBy: answeredBy(flow, step),
            hasWaitEvent,
          },
        },
      ],
    })
  }

  const failedNow = current?.execute.status === 'failed'
  const attempts =
    !hasExecuteEvent || current === null || attemptCount <= 0
      ? []
      : Array.from({ length: attemptCount }, (_, index) => ({
          n: index + 1,
          status:
            index === attemptCount - 1
              ? current.execute.status
              : ('failed' as PhaseStatus),
          failure: current.lastFailure,
        }))
  const executeDefault: PhaseTabId = hasExecuteEvent ? 'input' : 'output'
  phases.push({
    id: 'execute',
    label: 'Execute',
    defaultTab: executeDefault,
    tabs: [
      { id: 'input', label: 'Input', body: { kind: 'stepMethod', part: 'input' } },
      { id: 'context', label: 'Context', body: { kind: 'stepMethod', part: 'context' } },
      {
        id: 'output',
        label: 'Output',
        body: {
          kind: 'executeOutput',
          attempts,
          attemptsLeft:
            current === null || !hasExecuteEvent
              ? '—'
              : failedNow
                ? 'no retries left'
                : current.execute.status === 'completed'
                  ? 'finished'
                  : 'not retrying',
          decision: current?.decisionType ?? null,
          nextStepTypes: current?.nextStepTypes ?? [],
          hasExecuteEvent,
          error: current?.lastFailure,
        },
      },
    ],
  })

  const defaultPhase = phases.find((phase) => phase.id === 'execute' && hasExecuteEvent)
    ?? phases.find((phase) => phase.id === 'wait')
    ?? phases[0]
  const defaultSection = sectionId(defaultPhase.id, defaultPhase.defaultTab)

  return {
    title: step.label,
    stepType: step.stepType,
    defaultSection,
    definition,
    executions: execs.map((execution) => ({
      id: execution.stepExecutionId,
      wait: execution.waitFor === null ? 'no wait' : statusWord(execution.waitFor.status),
      execute: statusWord(execution.execute.status),
      won: waitKind(execution, [execution]) ?? '—',
    })),
    phases,
  }
}
