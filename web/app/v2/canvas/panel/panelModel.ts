// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowHistoryEvent } from '@/lib/types'
import type { PocFlow, StepModel } from '../model/pocFlow'
import type { PhaseStatus, RunOverlay } from '../model/run'
import {
  cardStatus,
  coarseElapsed,
  executionsOf,
  PHASE_LABEL,
  waitKind,
} from '../model/run'
import { answeredBy } from '../views/stepBox'

export type SectionId =
  | 'overview'
  | 'input'
  | 'output'
  | 'context'
  | 'wait'
  | 'execute'
  | 'executions'
  | 'transitions'
  | 'state'
  | 'definition'

export interface Row {
  label: string
  value: string
  tone?: 'normal' | 'quiet' | 'warn' | 'good'
  /** Raw text that must not be truncated — full guards, full error messages. */
  pre?: boolean
}

export type Availability = 'available' | 'notRetained' | 'tooLarge' | 'deleted'

export const AVAILABILITY_TEXT: Record<Availability, string> = {
  available: '',
  notRetained: 'Not retained — the run skeleton outlives its payloads.',
  tooLarge: 'Too large to display.',
  deleted: 'These payloads have been deleted.',
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

export type SectionBody =
  | { kind: 'rows'; rows: Row[]; availability?: Availability }
  | { kind: 'stepMethod'; part: 'input' | 'output' | 'context' }
  | { kind: 'wait'; rows: WaitRow[]; winner: string | null; answeredBy: string[] }
  | {
      kind: 'execute'
      attempts: AttemptRow[]
      attemptsLeft: string
      decision: string | null
      nextStepTypes: string[]
      hasExecuteEvent: boolean
      defaultTab: 'output' | 'error'
      io: Availability
      error?: string
    }
  | { kind: 'executions'; rows: { id: string; wait: string; execute: string; won: string }[] }
  | { kind: 'transitions'; inbound: Row[]; outbound: Row[] }

export interface Section {
  id: SectionId
  label: string
  body: SectionBody
}

export interface PanelModel {
  title: string
  stepType: string
  defaultSection: SectionId
  sections: Section[]
}

export interface ExecutionPayload {
  input: unknown
  output: unknown
  context: unknown
}

function statusWord(s: PhaseStatus): string {
  return PHASE_LABEL[s]
}

function traversals(overlay: RunOverlay | null): Map<string, number> {
  const out = new Map<string, number>()
  if (overlay === null) return out
  for (const e of overlay.executions) {
    for (const t of e.nextStepTypes ?? []) {
      const k = `${e.stepType}->${t}`
      out.set(k, (out.get(k) ?? 0) + 1)
    }
  }
  return out
}

export function buildPanel(
  flow: PocFlow,
  step: StepModel,
  overlay: RunOverlay | null,
  selectedExecutionId: string | null,
  methodEvent: FlowHistoryEvent | null = null,
  attemptCount = 0,
  hasExecuteEvent = false,
): PanelModel {
  const execs = overlay === null ? [] : executionsOf(overlay, step.stepType)
  const current =
    execs.find((e) => e.stepExecutionId === selectedExecutionId) ?? execs[execs.length - 1] ?? null
  const hasMethodEvent = methodEvent !== null

  const sections: Section[] = []

  const overview: Row[] = [{ label: 'Role', value: step.actor }]
  if (step.isStart) overview.push({ label: 'Start', value: 'this is where the flow begins' })
  if (current !== null) {
    overview.push({ label: 'Status', value: statusWord(cardStatus(current)) })
    overview.push({ label: 'Execution', value: current.stepExecutionId })
    if (execs.length > 1) overview.push({ label: 'Ran', value: `${execs.length} times` })
    if (current.startedAt !== undefined) {
      overview.push({
        label: current.endedAt === undefined ? 'Open for' : 'Took',
        value: coarseElapsed(current.startedAt, current.endedAt ?? overlay?.now ?? 0),
      })
    }
    const w = current.waitFor
    if (w !== null && (w.status === 'waiting' || w.status === 'pending')) {
      const pending = w.conditions.filter((c) => !c.satisfied)
      overview.push({
        label: 'Blocked on',
        value:
          pending.length === 0
            ? 'something the analyser did not name'
            : pending.map((c) => `${c.label} (${c.kind})`).join(', '),
        tone: 'warn',
      })
      const who = answeredBy(flow, step)
      if (who.length > 0) {
        overview.push({ label: 'Answered through', value: who.join(', '), tone: 'good' })
      }
    }
    if (current.lastFailure !== undefined) {
      overview.push({ label: 'Failure', value: current.lastFailure, tone: 'warn', pre: true })
    }
  } else if (overlay !== null) {
    overview.push({ label: 'Status', value: 'never ran in this run', tone: 'quiet' })
  }
  sections.push({ id: 'overview', label: 'Overview', body: { kind: 'rows', rows: overview } })

  if (hasMethodEvent) {
    sections.push({ id: 'input', label: 'Input', body: { kind: 'stepMethod', part: 'input' } })
    sections.push({ id: 'output', label: 'Output', body: { kind: 'stepMethod', part: 'output' } })
    sections.push({ id: 'context', label: 'Context', body: { kind: 'stepMethod', part: 'context' } })
  }

  if (step.waitFor !== null) {
    const declared = step.waitFor.conditions
    const live = current?.waitFor?.conditions ?? []
    const blocked = current?.waitFor?.status === 'waiting' || current?.waitFor?.status === 'pending'
    const anyWon = live.some((c) => c.satisfied)
    const rows: WaitRow[] = declared.map((d) => {
      const l = live.find((c) => c.label === d.label)
      let verdict: Verdict = 'notEvaluated'
      if (l !== undefined) {
        if (l.satisfied) verdict = 'won'
        else if (blocked) verdict = 'pending'
        else if (anyWon) verdict = 'lostStillQueued'
      }
      return { kind: d.kind, label: d.label, verdict }
    })
    if (declared.length === 0) {
      rows.push({ kind: 'unknown', label: 'not reported by the analyser', verdict: 'notEvaluated' })
    }
    sections.push({
      id: 'wait',
      label: `Wait · ${step.waitFor.type}`,
      body: {
        kind: 'wait',
        rows,
        winner: live.find((c) => c.satisfied)?.label ?? null,
        answeredBy: answeredBy(flow, step),
      },
    })
  }

  const failedNow = current?.execute.status === 'failed'
  const attempts =
    !hasExecuteEvent || current === null || attemptCount <= 0
      ? []
      : Array.from({ length: attemptCount }, (_, i) => ({
          n: i + 1,
          status:
            i === attemptCount - 1
              ? current.execute.status
              : ('failed' as PhaseStatus),
          failure: current.lastFailure,
        }))
  sections.push({
    id: 'execute',
    label: 'Execute',
    body: {
      kind: 'execute',
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
      defaultTab: failedNow ? 'error' : 'output',
      io: hasMethodEvent ? 'available' : overlay === null ? 'notRetained' : 'notRetained',
      error: current?.lastFailure,
    },
  })

  if (execs.length > 1) {
    sections.push({
      id: 'executions',
      label: `Executions · ${execs.length}`,
      body: {
        kind: 'executions',
        rows: execs.map((e) => ({
          id: e.stepExecutionId,
          wait: e.waitFor === null ? 'no wait' : statusWord(e.waitFor.status),
          execute: statusWord(e.execute.status),
          won: waitKind(e, [e]) ?? '—',
        })),
      },
    })
  }

  const trav = traversals(overlay)
  const inbound: Row[] = flow.transitions
    .filter((t) => t.toStepId === step.id)
    .map((t) => {
      const from = flow.steps.find((s) => s.id === t.fromStepId)
      const n = trav.get(`${from?.stepType}->${step.stepType}`)
      return {
        label: from?.label ?? t.fromStepId,
        value:
          (t.kind === 'failure_transition' ? 'on failure' : 'transition') +
          (n === undefined ? '' : ` · taken ${n}×`),
        tone: t.kind === 'failure_transition' ? ('warn' as const) : undefined,
      }
    })
    .sort((a, b) => a.label.localeCompare(b.label))
  const outbound: Row[] = flow.transitions
    .filter((t) => t.fromStepId === step.id)
    .map((t) => {
      const to = flow.steps.find((s) => s.id === t.toStepId)
      const n = trav.get(`${step.stepType}->${to?.stepType}`)
      return {
        label: to?.label ?? t.toStepId,
        value:
          (t.mergedGuards.length > 0 ? t.mergedGuards.join('  ·  ') : 'unconditional') +
          (n === undefined ? '' : ` · taken ${n}×`),
        pre: t.mergedGuards.length > 0,
      }
    })
  sections.push({ id: 'transitions', label: 'Transitions', body: { kind: 'transitions', inbound, outbound } })

  if (step.resources.length > 0) {
    sections.push({
      id: 'state',
      label: 'State',
      body: {
        kind: 'rows',
        rows: step.resources.map((r) => ({
          label: r.resourceId.replace(/^(attribute|channel|stream):/, ''),
          value: `${r.access}${r.phase === undefined ? '' : ` · during ${r.phase}`}`,
          tone: 'quiet' as const,
        })),
        availability: overlay === null ? undefined : hasMethodEvent ? 'available' : 'notRetained',
      },
    })
  }

  const defRows: Row[] = [{ label: 'Step type', value: step.stepType }]
  if (step.waitFor !== null) {
    defRows.push({ label: 'Wait', value: `${step.waitFor.type} · ${step.waitFor.sentence}` })
  }
  for (const b of step.execute.branches) {
    defRows.push({
      label: b.decisionTypes.join('/'),
      value: b.fullGuards.length > 0 ? b.fullGuards.join('  ·  ') : 'unconditional',
      pre: b.fullGuards.length > 0,
    })
  }
  if (step.span) {
    defRows.push({
      label: 'Source',
      value: `${flow.source.path}:${step.span.startLine}–${step.span.endLine}`,
      tone: 'quiet',
    })
  }
  sections.push({ id: 'definition', label: 'Definition', body: { kind: 'rows', rows: defRows } })

  return {
    title: step.label,
    stepType: step.stepType,
    defaultSection: hasMethodEvent ? 'input' : 'overview',
    sections,
  }
}
