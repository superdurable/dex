// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentRole } from '../model/agentic'
import type { Actor, PocFlow, StepModel } from '../model/pocFlow'
import { discriminantOf, fanOutOf } from '../model/routing'
import type { PhaseStatus } from '../model/run'
import type { CardToken, Reason } from '../model/run'
import {
  barDecode,
  cardStatus,
  cardToken,
  executionsOf,
  reasonLine,
  runStateFor,
  terminalReason,
} from '../model/run'
import type { Box, BoxRow, ViewOpts } from './types'

/**
 * The Step card, shared by every view that draws Steps.
 *
 * THE RULE, taken from every product surveyed: a node carries IDENTITY plus ONE STATUS TOKEN, and
 * the only extra text is a TYPE — never a value. n8n shows an operation subtitle, Airflow shows the
 * operator name, Zapier shows the app event. None of them puts a parameter, an expression or a
 * datum on a node; all of that is one click away in a panel that does not displace the graph.
 *
 * So this card no longer renders guards. It used to show `context.has_timer_fired()` and
 * `not (context.has_ti…` — source code on a canvas — which is the single least legible thing the
 * POC did. The Execute section now says WHERE the step can go, not WHY; the why is on the edge
 * label when you select an edge, and in the detail panel.
 *
 * Height is a function of the Step's own ANATOMY and the detail level, never of the zoom
 * transform. That keeps size structural, so changing detail is a repaint of known geometry rather
 * than an unpredictable reflow.
 */

export const STEP_W = 244

/**
 * Height the reason strip needs: 3 padding-top + 1 border-top + 14 line (10.5px at 1.35) + 2 flex gap,
 * rounded up. Measured against the DOM after the first two guesses clipped it — the same analytic
 * height bug has now bitten three times, always taking the LAST element with it, so it is pinned by a
 * test rather than trusted.
 */
export const REASON_H = 22

const CONDITION_GLYPH: Record<string, string> = {
  channel: '✉',
  timer: '⏱',
  subflow: '↳',
  unknown: '?',
}

/** What a Step IS, in the subtitle slot. The type, in plain words. */
const ROLE_PHRASE: Record<Actor, string> = {
  external: 'waits for someone, then runs',
  clock: 'waits for time, then runs',
  child: 'starts child flows, then runs',
  unknown: 'waits, then runs',
  machine: 'runs',
}

/**
 * The two branching shapes, as marks on the card.
 *
 * Both glyphs are from Geometric Shapes, the same block as the `■` the close rows already use, so
 * neither depends on a font the app has not already proved it can render.
 *
 * `▮` IS UML's FORK: the spec draws one as "a short heavy bar", and this is the closest a text glyph
 * gets. `◇` is UML's choice pseudostate. Borrowing the two notations means the marks are learnable
 * from any diagram the reader has met before, and it keeps the distinction in SHAPE rather than in
 * colour — colour on this canvas already means who must act, and the branching shape is orthogonal
 * to that.
 */
const FORK_GLYPH = '▮'
const CHOICE_GLYPH = '◇'

/**
 * WHAT AN AGENT STEP IS, in one row, in plain words.
 *
 * The decision step gets a filled diamond against the choice row's hollow one: UML's choice pseudostate
 * says "one of these paths", and filling it says "and something decided WHICH at run time". That is the
 * distinction the whole feature turns on, and it is a shape difference so it survives at thumbnail size
 * and under any colour deficiency.
 *
 * `capability` gets NO ROW. Eight cards each captioned "a thing the agent can do" is furniture — being
 * reachable from the decision step already says it, and the decision card says it for all of them.
 */
const AGENT_ROLE_ROW: Partial<Record<AgentRole, { glyph: string; text: string }>> = {
  decision: { glyph: '◆', text: 'the agent decides here' },
  'human-gate': { glyph: '✋', text: 'hands over to a person' },
  intake: { glyph: '⬡', text: 'where the request arrives' },
}

const CLOSE_TEXT: Record<string, string> = {
  complete: 'completes the flow',
  fail: 'fails the flow',
  deadEnd: 'stops this branch',
  completeIfChannelsEmpty: 'completes if the queues are empty',
}

function targetName(flow: PocFlow, stepId: string): string {
  return flow.steps.find((s) => s.id === stepId)?.label ?? stepId.replace(/^step:/, '')
}

/**
 * Which entry points answer this Step's gate.
 *
 * The wire encodes `rpc -publish-> channel` and `channel -condition-> wait` but never joins them,
 * so every RPC is graph-isolated. This walks the join in reverse. It is the most decision-relevant
 * fact on a gate card and the reason Channels do not need to be nodes: the name plus the answerer
 * fit on two rows of the step that is actually waiting.
 */
export function answeredBy(flow: PocFlow, step: StepModel): string[] {
  const channels = new Set(
    (step.waitFor?.conditions ?? [])
      .filter((c) => c.kind === 'channel' && c.resourceId !== undefined)
      .map((c) => c.resourceId as string),
  )
  if (channels.size === 0) return []
  const out = new Set<string>()
  for (const entry of flow.entries) {
    for (const gate of entry.opensGates) {
      if (channels.has(gate.channelId)) out.add(entry.name)
    }
  }
  return [...out]
}

export interface StepContent {
  rows: BoxRow[]
  sections: { label: string; rows: BoxRow[] }[]
  badge?: string
  status?: string
  subtitle: string
  height: number
  bar?: { status: PhaseStatus; count: number }[]
  barTitle?: string
  emphasis?: 'active' | 'failed' | 'planned'
  token?: CardToken
  reason?: Reason
  /** BPMN's loop marker: this step can transition to itself. */
  loops?: boolean
  /** Which kind of shared failure target this is, when it is one. */
  recovery?: { glyph: string; title: string }
}

export function stepContent(flow: PocFlow, step: StepModel, opts: ViewOpts): StepContent {
  const run = opts.run === null ? null : runStateFor(opts.run, step.stepType)

  /**
   * The ACTIVE step shows what it is waiting on regardless of the detail control.
   *
   * A targeted exception, not a general one. During a run the single actionable fact is what the open
   * step needs, and making someone expand to find it is the mistake Copilot Studio's docs complain
   * about when they tell you to collapse the activity map at every turn. The control still governs
   * every other card, so this does not quietly take the setting away.
   */
  const isOpen =
    run !== null && !run.planned && ['waiting', 'running', 'pending'].includes(cardStatus(run.current))
  const expanded = opts.detail === 'expanded' || isOpen
  const rows: BoxRow[] = []
  const sections: StepContent['sections'] = []

  if (run !== null && run.planned) {
    // Named by a completed execution's decision, but not started. Drawn because a run that cannot
    // say what is next is permanently one step behind.
    rows.push({ glyph: '·', text: 'not started yet', tone: 'quiet' })
  }

  if (run !== null && !run.planned) {
    const e = run.current
    // Attempts and the failure message moved INTO the reason line — a card that states the same fact
    // twice is how it grows into a panel. Only genuinely additive rows survive here.
    if (e.fromStepExecutionId?.startsWith('__rpc/') === true) {
      rows.push({
        glyph: '⬡',
        text: `started by ${e.fromStepExecutionId.replace('__rpc/', '')}`,
        tone: 'quiet',
      })
    }
  }

  /**
   * WHICH BRANCHING SHAPE this Step is, when it is either. Both rows are `rows` rather than sections,
   * so they show at BOTH detail levels — the fact changes what the reader believes about execution,
   * and it must not be something you have to expand a card to find.
   *
   * THE FAN-OUT IS THE ONE THAT NEEDS MARKING. Measured across the fixtures: of the 14 steps with more
   * than one possible next step, 13 take one path and 1 takes all of them. A step that chooses already
   * declares itself by carrying a guard on every branch, while a step that runs everything looked
   * identical to a two-way conditional. That is the asymmetry the mark exists to fix. Both rows can
   * appear together rather than one instead of the other: a Step with both a `goToMany` and guarded
   * branches is doing both things, and suppressing either would misreport it.
   *
   * THE CARD IS THE ONLY PLACE EITHER FACT IS SAID. Putting the case values on the edges as well was
   * built and removed — in both real switches the value names its own destination, so the label
   * restated the card it pointed at. So this row carries the whole hoist, and the values stay in the
   * panel with the guards.
   *
   * It says the path DEPENDS ON the name rather than that the name decides every path, because a
   * strict majority is the bar and RecoveryGate has one branch that tests something else. See
   * `model/routing`.
   */
  const fanOut = fanOutOf(step)
  if (fanOut > 0) {
    rows.push({
      glyph: FORK_GLYPH,
      text: fanOut === 2 ? 'both paths run' : `all ${fanOut} paths run`,
      tone: 'quiet',
    })
  }
  const routesOn = discriminantOf(step)
  /**
   * ON AN AGENT DECISION STEP, THE ROLE ROW REPLACES THE DISCRIMINANT ROW.
   *
   * Both are true — it does route on `action` — but "the path depends on action" describes a switch
   * statement, and this is not one: nobody wrote the value. Saying "the agent decides here" is the more
   * useful of two true sentences, and printing both would invite the reader to look for where `action` is
   * set, which is the wrong mental model of the thing.
   */
  if (routesOn !== null && step.agentRole !== 'decision') {
    rows.push({ glyph: CHOICE_GLYPH, text: `the path depends on ${routesOn}`, tone: 'quiet' })
  }
  const roleRow = step.agentRole === undefined ? undefined : AGENT_ROLE_ROW[step.agentRole]
  if (roleRow !== undefined) {
    rows.push({ glyph: roleRow.glyph, text: roleRow.text, tone: 'quiet' })
  }

  if (expanded && step.waitFor !== null) {
    const waitRows: BoxRow[] = []
    // During a run, which condition WON is load-bearing: Dex consumes only from the selected
    // condition and leaves the losing branches queued, so "waiting on A or B" without saying which
    // fired misreports durable state.
    const won = new Set(
      run !== null && !run.planned
        ? (run.current.waitFor?.conditions ?? []).filter((c) => c.satisfied).map((c) => c.label)
        : [],
    )
    for (const c of step.waitFor.conditions) {
      if (c.kind === 'timer') continue
      waitRows.push({
        glyph: won.has(c.label) ? '✔' : (CONDITION_GLYPH[c.kind] ?? '?'),
        // The identifier, verbatim. This is the handle somebody has to publish to, so inventing
        // prose for it would misrepresent what has to happen.
        text: won.has(c.label) ? `${c.label} — this one arrived` : c.label,
        tone: c.kind === 'channel' ? 'normal' : 'quiet',
      })
    }
    // Who can answer it — the synthesized join. Only a gate has one.
    for (const name of answeredBy(flow, step)) {
      waitRows.push({ glyph: '⬡', text: `answered through ${name}`, tone: 'quiet' })
    }
    // The timer last, because it is the reassurance rather than the ask: it says the flow is not
    // stuck forever. Dify models exactly this as a first-class timeout edge.
    const timers = step.waitFor.conditions.filter((c) => c.kind === 'timer')
    if (timers.length > 0) {
      waitRows.push({
        glyph: '⏱',
        text:
          waitRows.length > 0
            ? `or ${timers.map((t) => t.label).join(' or ')}, then it moves on`
            : `waits ${timers.map((t) => t.label).join(' or ')}`,
        tone: 'quiet',
      })
    }
    if (step.waitFor.conditions.length === 0) {
      waitRows.push({ glyph: '?', text: 'not reported by the analyser', tone: 'warn' })
    }
    if (waitRows.length > 0) sections.push({ label: 'Waits for', rows: waitRows })
  }

  if (expanded) {
    // WHERE it can go. Not why. Duplicate targets collapse — several guards reaching one place is
    // one arrow on the canvas and a count in the panel.
    const targets = new Map<string, string | undefined>()
    const closes = new Set<string>()
    for (const b of step.execute.branches) {
      for (const t of b.targets) targets.set(t.stepId, t.multiplicity)
      if (b.closes !== undefined) closes.add(b.closes)
    }
    const goRows: BoxRow[] = []
    for (const [stepId, mult] of targets) {
      goRows.push({
        glyph: '→',
        text: targetName(flow, stepId) + (mult ?? ''),
        tone: stepId === step.id ? 'quiet' : 'normal',
      })
    }
    for (const c of closes) {
      goRows.push({ glyph: '■', text: CLOSE_TEXT[c] ?? c, tone: 'quiet' })
    }
    if (goRows.length > 0) sections.push({ label: 'Then goes to', rows: goRows })
  }

  /**
   * "needs you" means NOW during a run, and "ever" only in the definition.
   *
   * A gate whose wait has already been satisfied is running, not asking — and a badge that shouts for
   * attention on a step that is quietly working is the always-on-map mistake. Same "now versus ever"
   * distinction view F already applies when it filters to open gates. In definition mode the badge
   * stays, because there it is a true statement about the Step's nature.
   */
  const waitPending = run !== null && !run.planned && ['waiting', 'pending'].includes(run.current.waitFor?.status ?? '')
  // BPMN's loop marker. A self-transition is 21-of-96 across the corpus, so it is common enough that
  // it has to read at a glance rather than by following a line.
  const loops = flow.transitions.some((t) => t.isSelfLoop && t.fromStepId === step.id)
  const needsYouNow = run === null || waitPending
  const badge =
    step.actor === 'external' && needsYouNow
      ? 'needs you'
      : step.isHub
        ? `${step.inboundCount} steps return here`
        : step.recoveryRole === 'sink' || step.recoveryRole === 'dispatcher'
          ? `catches ${step.inboundFailureCount} failures`
          : undefined

  /**
   * The recovery mark, which says WHICH KIND of failure target this is.
   *
   * Dex has no recovery Step type — the whole primitive is one per-Step
   * `on_execute_failure_proceed_to(X)`, so a Step becomes a failure target only by being named by
   * others, and all the multiplicity lands on the receiving side. The three roles that produces want
   * different things said about them, and a count alone does not distinguish the two that matter:
   * whether the flow STOPS here or CARRIES ON from here.
   *
   * Drawn as a second mark rather than folded into the badge, because a gate can also be a dispatcher
   * and the badge can only hold one fact.
   */
  const onward = flow.transitions.filter(
    (t) => t.fromStepId === step.id && t.kind !== 'failure_transition' && t.toStepId !== step.id,
  ).length
  const recovery =
    step.recoveryRole === 'sink'
      ? {
          glyph: '⇥',
          title: `Catches failures from ${step.inboundFailureCount} steps, and the flow stops here`,
        }
      : step.recoveryRole === 'dispatcher'
        ? {
            glyph: '⇉',
            title: step.hasUniformFailurePolicy
              ? `Every step that can fail routes here (${step.inboundFailureCount}), and it can send the flow onward to ${onward} — click to reveal both`
              : `Catches failures from ${step.inboundFailureCount} steps, and can send the flow onward to ${onward} — click to reveal where`,
          }
        : undefined

  /**
   * Height is computed analytically, not measured — no ResizeObserver, no two-pass render, so a
   * Scene lays out identically in a test and in the browser.
   *
   * The figures below track `poc.css` and were corrected once against the real DOM: the first pass
   * under-measured by about 8px per card, which silently clipped the last row of the last section.
   * If a rule there changes a font size, line height or padding, change the matching number here.
   *
   *   14  .pbox padding (7 top + 7 bottom)
   *   18  .pbox-title at 12.5px
   *   15  .pbox-meta at 10.5px
   *    2  flex gap
   *   21  per section: 4 margin + 3 padding + 1 border + 13 label
   *   17  per row at 11px / 1.5
   */
  /**
   * The bar appears only when it shows a MIX.
   *
   * A single solid segment says nothing the "ran N times" row does not already say, so four
   * successes in a row got a decorative block. It earns its place exactly when the outcomes differed
   * — which is the case a count alone cannot express, and the reason Airflow's version has a 2px
   * floor per segment.
   */
  const bar = run !== null && !run.planned && run.bar.length > 1 ? run.bar : undefined

  // The two-cell token, and the count that rides beside it.
  const all = opts.run === null ? [] : executionsOf(opts.run, step.stepType)
  const token: CardToken | undefined =
    run === null || run.planned ? undefined : cardToken(run.current, all)

  /**
   * The reason line. Blocked or failed only, so a healthy Step carries no strip and a green run reads
   * quiet — and a surprising terminal decision (deadEnd, forceFail, forceComplete*) is named, while an
   * ordinary goTo never is.
   */
  const reason: Reason | undefined =
    run === null || run.planned
      ? undefined
      : (reasonLine(run.current, opts.run?.now ?? 0) ??
        terminalReason(run.current.decisionType) ??
        undefined)

  const barTitle =
    bar === undefined || opts.run === null ? undefined : barDecode(bar, all)

  /**
   * The count beside the token. SUPPRESSED at N≤1 — three products landed on that independently
   * (n8n `iterations > 1`, Airflow's Try Number, Inngest's Attempt badge), so `×1` never renders and
   * the token alone speaks for a single execution.
   *
   * But we deliberately BREAK n8n's second suppression, which hides the count entirely on an errored
   * node: for a Step absorbing 16 of 27 transitions, "ran 4 times, 1 failed" and "ran 4 times, 4
   * failed" are different incidents and the card has to distinguish them.
   */
  const failed = run === null || run.planned ? 0 : (run.bar.find((b) => b.status === 'failed')?.count ?? 0)
  const status =
    run === null
      ? undefined
      : run.planned
        ? 'next'
        : run.count <= 1
          ? undefined
          : `×${run.count}${failed > 0 ? ` · ${failed} failed` : ''}`

  const emphasis: StepContent['emphasis'] =
    run === null
      ? undefined
      : run.planned
        ? 'planned'
        : cardStatus(run.current) === 'failed'
          ? 'failed'
          : isOpen
            ? 'active'
            : undefined

  const head = 49
  const sectionsH = sections.reduce((acc, s) => acc + 23 + s.rows.length * 17, 0)
  const height = Math.max(
    52,
    head + rows.length * 17 + sectionsH + (bar === undefined ? 0 : 11) + (reason === undefined ? 0 : REASON_H),
  )

  return { rows, sections, badge, status, bar, barTitle, emphasis, token, reason, loops, recovery, subtitle: ROLE_PHRASE[step.actor], height }
}

export function stepBox(
  flow: PocFlow,
  step: StepModel,
  opts: ViewOpts,
  x: number,
  y: number,
): Box {
  const c = stepContent(flow, step, opts)
  return {
    id: step.id,
    x,
    y,
    w: STEP_W,
    h: c.height,
    kind: 'step',
    title: step.label,
    subtitle: c.subtitle,
    actor: step.actor,
    ...(step.agentRole === undefined ? {} : { agentRole: step.agentRole }),
    status: c.status,
    token: c.token,
    reason: c.reason,
    barTitle: c.barTitle,
    rows: c.rows,
    sections: c.sections,
    badge: c.badge,
    loops: c.loops,
    recovery: c.recovery,
    bar: c.bar,
    /**
     * During a run, run state wins the emphasis slot.
     *
     * Structural marks (start, hub) are about the DEFINITION and are still true, but "this failed" or
     * "this is what we are waiting on" is what a reader needs at that moment. Only a CONTROL hub gets
     * the structural warning anyway — a compensation target collecting failures is correct saga
     * structure, not a drawing problem.
     */
    emphasis: c.emphasis ?? (step.isStart ? 'start' : step.isHub ? 'hub' : undefined),
  }
}
