// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * AN AGENTIC RUN: a goal, some capabilities, and a run whose sequence nobody wrote.
 *
 * WHY THIS IS NOT A LONGER `RunOverlay`. An overlay answers "what state is each Step in". That is enough
 * for a deterministic flow, where the ORDER was written by an author and the drawing already shows it.
 * An agentic run's order is the interesting part: the same definition can produce a three-step run or a
 * fifteen-step one, and the question a supervisor asks is never "what state is CloseCase in" but "why did
 * it choose that, and what did it consider". None of that has anywhere to live in an overlay.
 *
 * So this sits BESIDE the overlay and PROJECTS INTO one. `projectOverlay` turns a trajectory plus a
 * position into the exact shape `stepBox`, `buildPanel` and `runLog` already consume, which is why the
 * canvas needed no new renderer: the live execution map is the existing map, painted from a projection.
 *
 * THE EXECUTION MODEL, stated once because everything below is shaped by it:
 *
 *     observe state -> decide next action -> execute -> observe result -> decide again
 *
 * A `decision` beat is the "decide" arrow made visible. It carries the context it weighed, the action it
 * chose, and every action it could have chosen instead — that last field is what makes it a decision
 * rather than a step in a list, and it is the difference the UI has to show.
 *
 * PROGRESS IS DERIVED, not accumulated, and that is load-bearing. `advance` is a pure function of
 * (trajectory, interventions, startedAt, now): there is no frame counter, nothing to reset, and no way
 * for the canvas, the chat and the panel to disagree about where the run is, because all three read the
 * same function. Pausing, waiting on a human and resuming are all expressed as inputs to it rather than
 * as mutations of it.
 *
 * SCRIPTED, AND HONEST ABOUT IT. There is no model here. The trajectory is written down, including the
 * alternate continuations for reject and override, so what the prototype demonstrates is the SHAPE of
 * agentic supervision rather than a claim about an agent's judgement.
 */

import type { PhaseStatus, RunOverlay, StepExecution } from './run'

/**
 * What a Step is FOR in an agentic flow.
 *
 * DERIVED FROM THE CHARTER AND THE GRAPH, and the first version was wrong about where it comes from.
 * It read `node.metadata.agentRole`, on the assumption that an author could annotate a Step — and then
 * running the real analyser settled it: `dexcli 0.6.0 visualize` emits NO metadata on a step node at
 * all. There is nowhere for a hint to ride, so it has to be worked out from things that do exist.
 *
 * Which turns out to be better. The charter already names the Step behind each capability, and the
 * graph already says which Step they all return to, so `agentRolesOf` reads the roles off two things
 * the author genuinely wrote rather than off an annotation they would have had to remember to add.
 * See `agentRolesOf`.
 */
export const AGENT_ROLES = ['intake', 'decision', 'capability', 'human-gate'] as const

export type AgentRole = (typeof AGENT_ROLES)[number]

export function isAgentRole(value: unknown): value is AgentRole {
  return typeof value === 'string' && (AGENT_ROLES as readonly string[]).includes(value)
}

/** The minimum a role derivation needs. A structural view, so this stays testable without a fixture. */
export interface RoleGraph {
  startStepId?: string
  steps: { id: string; waitFor: unknown }[]
  transitions: { fromStepId: string; toStepId: string }[]
}

/**
 * Which Step is which, from the charter and the graph. Empty when the flow is not agentic.
 *
 * THE DECISION STEP IS THE ONE THE CAPABILITIES RETURN TO. That is not a heuristic dressed up — it is
 * the definition of the loop: an agentic flow is capabilities hanging off one point that each hand
 * control back to it, so the Step with the most inbound edges FROM capabilities is that point. A flow
 * where no such Step exists is not agentic, and this returns nothing for it.
 *
 * THE HUMAN GATE IS A CAPABILITY THAT WAITS. Also structural rather than named: a Step whose WaitFor
 * blocks on something no other Step can satisfy is where the flow hands over, and Dex says so in
 * `phase` without anybody having to declare it.
 */
export function agentRolesOf(graph: RoleGraph, charter: AgentCharter | null): Map<string, AgentRole> {
  const roles = new Map<string, AgentRole>()
  if (charter === null) return roles

  const capabilities = new Set(charter.capabilities.map((c) => c.stepId))
  if (capabilities.size === 0) return roles

  const returnsTo = new Map<string, number>()
  for (const t of graph.transitions) {
    if (!capabilities.has(t.fromStepId) || t.toStepId === t.fromStepId) continue
    returnsTo.set(t.toStepId, (returnsTo.get(t.toStepId) ?? 0) + 1)
  }
  let decision: string | null = null
  let most = 0
  for (const [id, n] of returnsTo) {
    if (n > most) {
      decision = id
      most = n
    }
  }
  // Two, so one capability that happens to chain into another cannot be mistaken for the hub.
  if (decision === null || most < 2) return roles

  roles.set(decision, 'decision')
  for (const step of graph.steps) {
    if (!capabilities.has(step.id) || step.id === decision) continue
    roles.set(step.id, step.waitFor === null ? 'capability' : 'human-gate')
  }
  if (graph.startStepId !== undefined && !roles.has(graph.startStepId)) {
    roles.set(graph.startStepId, 'intake')
  }
  return roles
}

/* ------------------------------------------------------------ the charter */

/**
 * WHAT THE AUTHOR DEFINED, as opposed to what the run did.
 *
 * This is the whole of an agentic process definition: not a sequence, but a goal, a set of capabilities,
 * the boundaries, and how you know it worked. It belongs to EDIT mode — a runtime intervention must never
 * touch it — and it is what the run is judged against.
 *
 * It is model-authored per flow rather than carried on the wire, because the FDG has nowhere to put it:
 * `flow` has a name and a start step and nothing else. That is a gap in the contract, not a shortcut
 * here, and it is the same shape as `stepGroups` already being authored beside the graph.
 */
export interface AgentCharter {
  goal: string
  /** Each capability names the Step that performs it, so the list is clickable. */
  capabilities: { label: string; stepId: string }[]
  constraints: string[]
  successCriteria: string[]
}

/* ----------------------------------------------------------- the decision */

/** One fact the agent weighed. `cites` makes it a link rather than a line of text. */
export interface DecisionFact {
  label: string
  value: string
  /** An entity id — the attribute or step this fact came from. */
  cites?: string
  /** True when this fact is the reason the decision is hard. Drives emphasis, nothing else. */
  pivotal?: boolean
}

export interface AgentDecision {
  decisionId: string
  runId: string
  /** The decision Step this happened at. */
  stepId: string
  /** The question, in the agent's own voice. The panel's headline. */
  question: string
  /** What it observed, in the order it observed it. */
  context: DecisionFact[]
  /** What it proposes, in one sentence. */
  recommendation: string
  /** The capability it chose, as an entity id, so the canvas can light it. */
  chose: string
  /**
   * Every capability it could have chosen. §12's `availableActions`.
   *
   * Present even when the choice was obvious, because "it had eight options and took this one" is the
   * fact that distinguishes an agent from a switch statement, and it is invisible without this list.
   */
  availableActions: { action: string; stepId: string }[]
  /** The constraint that forced a human in, when one did. Quoted from the charter. */
  constraint?: string
  humanRequired: boolean
  status: 'settled' | 'awaiting' | 'overridden'
}

/* ------------------------------------------------------- human in the loop */

export const INTERVENTION_TYPES = [
  'approve',
  'reject',
  'override',
  'inform',
  'instruct',
] as const

export type InterventionType = (typeof INTERVENTION_TYPES)[number]

export interface HumanIntervention {
  interventionId: string
  runId: string
  type: InterventionType
  /** Verbatim: what the user clicked or typed. */
  input: string
  at: number
  /** The beat it answered, when it answered one. */
  beatId?: string
  /**
   * Which continuation it selected, for an intervention that changes what happens next.
   *
   * A KEY rather than free text, because the run has to be able to act on it. Free-form input is parsed
   * into one of these by `domain/run/intervene`; anything it cannot map becomes an `instruct` that the
   * agent acknowledges without changing course, which is the honest behaviour for a prototype with no
   * model behind it.
   */
  outcome?: string
  /** What the run did as a result, in words. Shown in the conversation. */
  resultingAction: string
}

/* ------------------------------------------------------------- trajectory */

/** What a human is being asked, and the buttons for it. */
export interface HumanAsk {
  prompt: string
  options: { value: string; label: string; tone?: 'primary' | 'danger' }[]
  /** The instruction under the buttons, for the free-form path. */
  hint?: string
  /**
   * WHAT THE RUN IS ACTUALLY PARKED ON, as the Channel or ChannelMap the Flow named.
   *
   * It used to be hardcoded to `manager-approval` one function down, which meant a gate waiting on the
   * CUSTOMER reported that a manager was needed — a case sitting in the wrong person's queue, and the
   * reason worse than the wrong label: a manager who clears it has answered a question nobody asked them.
   * Optional, and the fallback is deliberately non-committal rather than a guess at a role.
   */
  channel?: string
  /**
   * WHICH open question this is, so an answer cannot land on a different one.
   *
   * The Flow mints a key per gate entry and the graph carries only a placeholder for it, so the value has
   * to come from the run's own read model. Absent on a scripted trajectory, which is precisely why a
   * scripted approval cannot be mistaken for a live one.
   */
  requestKey?: string
  /** The RPC that admits an answer. Named on the ask so no caller has to know the Flow's surface. */
  rpc?: string
}

/**
 * ONE BEAT of a run: the agent decided, or the agent acted, or the run is waiting on a person.
 *
 * `ticks` rather than milliseconds so playback speed is one constant in one place, and a beat that
 * represents real work (issuing a refund) can be visibly slower than one that represents a lookup
 * without either being a magic number here.
 */
export interface Beat {
  id: string
  /** The Step this beat runs, as an entity id. */
  stepId: string
  stepType: string
  role: AgentRole
  /** What the agent says when this beat starts. Absent for a beat with nothing to report. */
  say?: string
  /** Present on a decision beat. */
  decision?: AgentDecision
  /** Present when the run cannot continue without a person. */
  awaits?: HumanAsk
  ticks?: number
  /**
   * WHAT HAPPENS NEXT, keyed by the outcome of the intervention that unblocks this beat.
   *
   * This is what makes reject and override real rather than cosmetic: approving continues down one list
   * of beats, rejecting down another, overriding down a third. The alternatives are scripted, which the
   * UI says out loud — but they are genuinely different execution, and the agent "deciding again after
   * observing the human's answer" is exactly what an agentic run does.
   */
  continuations?: Record<string, Beat[]>
}

export interface Trajectory {
  runId: string
  flowId: string
  /** The charter this run is executing against. */
  charter: AgentCharter
  /** The opening beats, up to and including the first thing that needs a person. */
  beats: Beat[]
  /** Facts about the case, for the panel's header. */
  subject: { label: string; value: string }[]
}

/* --------------------------------------------------------------- progress */

/** One beat of playback. Chosen so the whole demo trajectory reads in about ten seconds. */
export const TICK_MS = 900

export interface Progress {
  /** Beats that have finished, in order. */
  completed: Beat[]
  /** The beat in flight, or null when the run has finished every beat it has. */
  current: Beat | null
  /** Set when `current` cannot proceed without a person. */
  awaiting: HumanAsk | null
  /** True when the last beat of the resolved trajectory has completed. */
  resolved: boolean
  /**
   * NO `paused` FIELD, and its removal is KTD12 finishing.
   *
   * There was one, and nothing can set it any more: the chat vocabulary that minted a `pause` froze this
   * projection while the Dex Flow carried on — a Step already dispatched still ran, a timer still fired, an
   * effect still landed. A durable execution has no client-side pause, so the field described a state the
   * process could not be in, and six surfaces carried a branch for it that could never be taken.
   */
  /** The whole resolved beat list, so callers can count what is still to come. */
  beats: Beat[]
}

/**
 * The beat list this run actually has, given the interventions so far.
 *
 * Splices in the continuation each answered ask selected. Written as a fold rather than recursion so the
 * result is a FLAT list: everything downstream — progress, the overlay projection, the canvas path — walks
 * one array and none of them has to know the trajectory was ever a tree.
 */
export function resolveBeats(trajectory: Trajectory, interventions: HumanIntervention[]): Beat[] {
  const out: Beat[] = []
  let queue = [...trajectory.beats]
  const guard = 200
  for (let n = 0; n < guard && queue.length > 0; n += 1) {
    const beat = queue.shift() as Beat
    out.push(beat)
    if (beat.awaits === undefined || beat.continuations === undefined) continue
    const answer = answerFor(beat, interventions)
    if (answer === null) break // Nothing past an unanswered ask exists yet.
    /**
     * AN UNRECOGNISED OUTCOME ADVANCES NOTHING. R11, and the fallback it replaces was the single most
     * dangerous line in this model.
     *
     * It read `?? continuations['default'] ?? []`, and the fixture's `default` was the APPROVED branch — so
     * an answer whose outcome named no continuation issued the refund. That is reachable from a typo in a
     * parser pattern, from an option value the parser has no phrasing for, and from any future gate whose
     * options are added without continuations. Every one of those is a silent approval.
     *
     * Now the gate stays open instead. A caller that meant something specific has to name a continuation
     * that exists, and a caller that did not gets the state a supervisor can see and answer again — which
     * is the fail-closed direction for a control surface.
     */
    const named = answer.outcome === undefined ? undefined : beat.continuations[answer.outcome]
    if (named === undefined) break
    queue = [...named, ...queue]
  }
  return out
}

/**
 * The intervention that answers a given beat, or null.
 *
 * Matches on `beatId` first and falls back to the earliest intervention of an ANSWERING type that has
 * not been used by an earlier beat — because a user typing "approve" into the box has not named a beat,
 * and requiring them to would defeat the point of chat being the control surface.
 *
 * `instruct` is deliberately NOT an answering type: telling the agent something is not the same as deciding.
 */
const ANSWERING: InterventionType[] = ['approve', 'reject', 'override', 'inform']

export function answerFor(beat: Beat, interventions: HumanIntervention[]): HumanIntervention | null {
  const exact = interventions.find((i) => i.beatId === beat.id)
  if (exact !== undefined) return exact
  const loose = interventions.find((i) => i.beatId === undefined && ANSWERING.includes(i.type))
  return loose ?? null
}

/**
 * WHERE THE RUN IS, as a pure function of the clock and the interventions.
 *
 * The walk keeps its own clock rather than dividing elapsed time by a beat cost, and the reason is the
 * waiting: a run parked on an approval for four minutes must not then reveal four minutes' worth of beats
 * the instant it is approved. So an answered ask advances the clock to the moment the HUMAN answered, and
 * everything after it is timed from there. That one line is what makes "waiting" a real state rather than
 * a wait in an animation.
 */
export function advance(
  trajectory: Trajectory,
  interventions: HumanIntervention[],
  startedAt: number,
  now: number,
): Progress {
  const beats = resolveBeats(trajectory, interventions)
  const effectiveNow = now

  const completed: Beat[] = []
  let clock = startedAt

  for (const beat of beats) {
    if (beat.awaits !== undefined) {
      const answer = answerFor(beat, interventions)
      if (answer === null) {
        return { completed, current: beat, awaiting: beat.awaits, resolved: false, beats }
      }
      /**
       * AN ANSWER THAT SELECTED NO CONTINUATION LEAVES THE GATE OPEN, and this is the second half of the
       * unsafe-default fix — without it the first half traded one defect for a worse one.
       *
       * `resolveBeats` refuses to advance past an outcome that names no continuation. But this loop read
       * "there is an answer" and cleared `awaiting`, so the run reported that nobody was waiting on it while
       * nothing had happened: a case silently stalled, absent from the queue, with no one aware of it. A
       * gate that stays visibly open is recoverable; one that quietly closes is not.
       */
      const selected =
        answer.outcome !== undefined && beat.continuations?.[answer.outcome] !== undefined
      if (!selected && beat.continuations !== undefined) {
        return { completed, current: beat, awaiting: beat.awaits, resolved: false, beats }
      }
      // Resume from when the person answered. Anything else replays the wait as execution.
      clock = Math.max(clock, answer.at)
      completed.push(beat)
      continue
    }
    const cost = Math.max(1, beat.ticks ?? 1) * TICK_MS
    if (effectiveNow < clock + cost) {
      return { completed, current: beat, awaiting: null, resolved: false, beats }
    }
    clock += cost
    completed.push(beat)
  }

  return { completed, current: null, awaiting: null, resolved: true, beats }
}

/* ------------------------------------------------------- overlay projection */

/**
 * The trajectory, as the overlay the canvas already knows how to paint.
 *
 * THIS IS THE WHOLE REASON THERE IS NO SECOND RENDERER. A `RunOverlay` is what `stepBox` reads for card
 * status, what `buildPanel` reads for execution detail, and what `runLog` narrates — so an agentic run
 * that projects into one gets the live execution map, the step panel and the log for free, and the canvas
 * stays the canvas.
 *
 * ONE EXECUTION PER BEAT, not per Step. A capability the agent used twice has two executions, and the
 * decision Step in the refund run has three, which is exactly the cardinality the unified view was built
 * for: `runStateFor` rolls them into one card with a count and a state bar.
 */
/**
 * @param startedAt when the run began, as an absolute instant. Defaults to `now` minus the elapsed cost of
 * everything walked, which is the best available answer when a caller has no start to give.
 */
export function projectOverlay(
  trajectory: Trajectory,
  progress: Progress,
  now: number,
  startedAt?: number,
): RunOverlay {
  const ordinals = new Map<string, number>()
  const nextOrdinal = (stepType: string): number => {
    const n = (ordinals.get(stepType) ?? 0) + 1
    ordinals.set(stepType, n)
    return n
  }

  const executions: StepExecution[] = []
  /**
   * THE CLOCK STARTS AT THE RUN'S START, ABSOLUTELY. It used to start at zero.
   *
   * That made every `StepExecution.startedAt` an OFFSET from the run's beginning while `overlay.now`, and
   * every hand-written overlay in the fixtures, is an absolute instant. Two units in one field, and the
   * mismatch was silent because nothing in the canvas subtracts them — it only orders them.
   *
   * Where it surfaced: the Run Audit Trail derives a job's `startedAt` from `executions[0].startedAt`, so
   * the refund-agentic jobs were stamped at the Unix epoch and the trail reported a refund **20,335 days
   * old**. `?? canonical.now` did not catch it either, because `0` is not nullish.
   *
   * Defaulting to `now - totalCost` rather than to `0` means a caller that cannot supply a start still gets
   * absolute values in the right units. Approximate for a run parked on a human gate — the wait's true
   * duration is unknowable from the beat list — and approximate in the right unit beats exact in the wrong
   * one.
   */
  const totalCost = [...progress.completed, ...(progress.current === null ? [] : [progress.current])]
    .reduce((sum, b) => sum + Math.max(1, b.ticks ?? 1) * TICK_MS, 0)
  let at = startedAt ?? now - totalCost

  const push = (beat: Beat, kind: 'done' | 'current'): void => {
    const ordinal = nextOrdinal(beat.stepType)
    const cost = Math.max(1, beat.ticks ?? 1) * TICK_MS
    const gate = beat.awaits !== undefined
    /**
     * A HUMAN GATE IN FLIGHT IS `waiting`, NOT `running` — the distinction the whole product turns on.
     * Dex's own phase split says it: the Wait is open and the Execute has not started, so the card reads
     * achromatic and the reason line says what it is waiting for rather than looking busy.
     */
    const waitStatus: PhaseStatus = kind === 'done' ? 'completed' : 'waiting'
    const execStatus: PhaseStatus =
      kind === 'done' ? 'completed' : gate ? 'notStarted' : 'running'
    executions.push({
      stepExecutionId: `${beat.stepType}-${ordinal}`,
      stepType: beat.stepType,
      ordinal,
      /**
       * The condition comes from the BEAT, not from this function.
       *
       * It was hardcoded to `manager-approval`, so every gate — including one waiting on a customer reply —
       * announced that a manager was needed. A wait condition is a fact about the Flow, and the only thing
       * here that knows it is the beat.
       */
      waitFor: gate
        ? {
            status: waitStatus,
            conditions: [
              {
                kind: 'channel',
                label: beat.awaits?.channel ?? 'an answer',
                satisfied: kind === 'done',
              },
            ],
          }
        : null,
      execute: { status: execStatus },
      attempts: 1,
      startedAt: at,
      ...(kind === 'done' ? { endedAt: at + cost } : {}),
      /**
       * WHAT IT DECIDED TO DO NEXT, which is what lets the canvas draw the next Step before it exists —
       * and for an agentic run it is more than a nicety: "the agent has chosen SearchRefundPolicy" is the
       * output of the decision, so without it the map is permanently one beat behind the reasoning.
       */
      ...(kind === 'done' && beat.decision !== undefined
        ? { nextStepTypes: [beat.decision.chose.replace(/^step:/, '')] }
        : {}),
      ...(kind === 'done' ? { decisionType: 'goTo' } : {}),
    })
    at += cost
  }

  for (const beat of progress.completed) push(beat, 'done')
  if (progress.current !== null) push(progress.current, 'current')

  return {
    runId: trajectory.runId,
    flowId: trajectory.flowId,
    status: progress.resolved ? 'completed' : 'running',
    simulated: true,
    note: 'Scripted agent decisions. No model was called; the trajectory and its alternatives are written down.',
    now: Math.max(now, at),
    executions,
  }
}

/**
 * THE PATH THIS RUN ACTUALLY TOOK, as consecutive step-id pairs.
 *
 * Pairs rather than a set of steps, because the criterion is that the PATH is highlighted and a set
 * cannot express a path: the agentic definition has an edge from the decision Step to all eight
 * capabilities, and which of them were traversed — and in what order — is the whole story of the run.
 * A capability used twice yields two entries, so a loop reads as a loop.
 */
export function executionPath(progress: Progress): { from: string; to: string; ordinal: number }[] {
  const seen = [...progress.completed, ...(progress.current === null ? [] : [progress.current])]
  const out: { from: string; to: string; ordinal: number }[] = []
  for (let i = 1; i < seen.length; i += 1) {
    const from = seen[i - 1] as Beat
    const to = seen[i] as Beat
    out.push({ from: from.stepId, to: to.stepId, ordinal: i })
  }
  return out
}

/** Every decision this run has made, newest last. The panel picks the one that matters. */
export function decisionsOf(progress: Progress): AgentDecision[] {
  const all = [...progress.completed, ...(progress.current === null ? [] : [progress.current])]
  return all.map((b) => b.decision).filter((d): d is AgentDecision => d !== undefined)
}

/**
 * THE ONE THING THE PANEL LEADS WITH.
 *
 * The decision the run is stopped on if it is stopped on one, else the most recent decision made. That
 * order matters: a run waiting on a person has exactly one thing worth reading, and showing the latest
 * settled decision instead would bury the ask under history.
 */
export function currentDecision(progress: Progress): AgentDecision | null {
  if (progress.current?.decision !== undefined) return progress.current.decision
  const settled = decisionsOf(progress)
  return settled.at(-1) ?? null
}

/**
 * A DETERMINISTIC RUN, as a `Progress` — so the run panel can describe one too.
 *
 * The projection runs the other way here: `projectOverlay` turns beats into an overlay for the canvas, and
 * this turns an overlay into beats for the panel. Both exist because the two surfaces want different shapes
 * of the same execution, and one conversion each is cheaper than teaching either surface both shapes.
 *
 * IT MINTS NO DECISIONS, and that is the honest part. A fixed sequence has none — every transition was
 * written by the author — so the panel leads with the trail and says so. Manufacturing a "decision" per Step
 * would be exactly the false equivalence the two variants exist to disprove.
 */
export function progressFromOverlay(overlay: RunOverlay): Progress {
  const open: PhaseStatus[] = ['waiting', 'pending', 'running', 'notStarted']
  const beats: Beat[] = overlay.executions.map((e) => ({
    id: e.stepExecutionId,
    stepId: `step:${e.stepType}`,
    stepType: e.stepType,
    role: e.waitFor === null ? 'capability' : 'human-gate',
  }))
  const lastOpen = overlay.executions.findIndex(
    (e) => open.includes(e.execute.status) || open.includes(e.waitFor?.status ?? 'completed'),
  )
  const cut = lastOpen === -1 ? beats.length : lastOpen
  return {
    completed: beats.slice(0, cut),
    current: beats[cut] ?? null,
    awaiting: null,
    resolved: lastOpen === -1,
    beats,
  }
}
