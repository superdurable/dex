// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentRole } from '../model/agentic'
import type { Actor, PocFlow } from '../model/pocFlow'
import type { CardToken, PhaseStatus, Reason, RunOverlay } from '../model/run'
import type { StepGroup } from './groups'

/**
 * A Scene is plain geometry. It is the boundary the whole design rests on: every view produces
 * one, the renderer consumes nothing else, and no React Flow type appears above
 * `render/Stage.tsx`.
 */

/**
 * Level of detail. TWO levels, and it is an EXPLICIT control — never derived from zoom.
 *
 * This replaced a four-tier zoom-driven ladder, for two reasons found in research:
 *
 *  1. The top tier died on its own. It carried guard expressions, source spans and resource
 *     access — all of which are VALUES, and every surveyed product keeps values in a panel and
 *     puts only identity plus one status token on a node. Once the cards lost the code there was
 *     nothing left for that tier to show.
 *  2. The bottom tier was never a detail level either. "See the whole flow at once" is geometric
 *     zoom, not different content. n8n makes that work by raising edge contrast as you zoom out
 *     and counter-scaling chrome so labels do not shrink with the graph — cheaper than a tier,
 *     and it does not reflow.
 *
 * What is left matches what the industry actually ships: Copilot Studio's level-of-detail control
 * is literally `Expand/Collapse`, listed as toolbar item 1 with zoom as items 2-3. Nine products
 * were surveyed and NONE ties detail to zoom.
 */
export type Detail = 'collapsed' | 'expanded'

export const DETAILS: Detail[] = ['collapsed', 'expanded']

export const DETAIL_LABEL: Record<Detail, string> = {
  collapsed: 'Collapsed',
  expanded: 'Expanded',
}

/**
 * Everything a view needs.
 *
 * Deliberately tiny. It used to also carry six overlay flags, an edge-meaning mode and two
 * three-way reveal controls; all of them were removed rather than tuned:
 *
 *  - Streams, Attributes and Channels are never nodes (no surveyed product draws state or
 *    transport as a node), so there was nothing to toggle.
 *  - RPC entries fold into the gate row except the ones that start a Step, which always draw.
 *  - SubFlows are a boundary of the world, so they are always on.
 *  - Recovery is always drawn and routed aside — nobody hides failure paths behind a toggle.
 *  - Diagnostics is a panel, not an overlay.
 *  - With resources gone as nodes, "what do the arrows mean" has only one answer left.
 */
/**
 * Layout direction for the topology view.
 *
 * A setting rather than a decision, following Airflow — which persists graph direction per graph
 * precisely because no single answer wins: long pipelines want left-to-right, wide fan-out wants
 * top-to-bottom. n8n hardcodes LR for its main flow, Zapier hardcodes TB. Our real flow is a loop
 * with a seven-way fan, so this is here to be judged rather than asserted.
 *
 * The cost of making it a setting, worth stating: it forfeits Zapier-style positional semantics,
 * where "far right" can *mean* fallback because position is never the user's to change.
 */
export type Direction = 'tb' | 'lr'

export const DIRECTIONS: Direction[] = ['tb', 'lr']

export const DIRECTION_LABEL: Record<Direction, string> = {
  tb: 'Top-down',
  lr: 'Left-right',
}

export interface ViewOpts {
  detail: Detail
  direction: Direction
  selectedId: string | null
  /**
   * Run state, or null for the definition.
   *
   * Null means the ABSENCE of a run, not a run with every status blank. A definition renders every
   * possible path; a graph where every node read "pending" would misrepresent what the artifact is.
   * So the same views serve both, and execution is strictly additive.
   */
  run: RunOverlay | null
  /**
   * THE PATH THIS RUN ACTUALLY TOOK, as consecutive step-id pairs in order.
   *
   * A LIST OF PAIRS, not a set of steps, and the difference is the whole feature: an agentic definition has
   * an edge from its decision Step to all eight capabilities, so "which steps ran" says nothing about which
   * arrows were followed, in what order, or which were followed twice. A run that looped through the
   * decision Step three times is a path with three visits and a set with one.
   *
   * Empty for a deterministic run, where the drawing already is the path.
   */
  path?: { from: string; to: string; ordinal: number }[]
  /**
   * Semantic groups, supplied by the caller rather than derived here.
   *
   * Passed in so the view stays a pure function of the flow: the groups are currently mocked per
   * fixture, and a view that looked up a fixture id would be coupled to the harness.
   */
  groups?: StepGroup[]
}

export type BoxKind =
  | 'step'
  | 'rpc'
  | 'timeoutHandler'
  | 'attribute'
  | 'channel'
  | 'stream'
  | 'subflow'
  | 'commit'
  | 'gate'
  | 'summary'
  | 'note'

export interface BoxRow {
  /** A small leading glyph. Text, not an icon font. */
  glyph?: string
  text: string
  /** Right-aligned trailing text. Identity only — never an expression. */
  trail?: string
  tone?: 'normal' | 'quiet' | 'warn'
}

export interface Box {
  id: string
  x: number
  y: number
  w: number
  h: number
  kind: BoxKind
  title: string
  /** A stable semantic icon supplied by the definition, never inferred from the Step name. */
  icon?: 'connector'
  /** The TYPE, never a value. Airflow shows the operator name here; we show the Step's role. */
  subtitle?: string
  /**
   * Drives the accent that says who has to act. Taken from the domain rather than restated, so
   * adding an actor cannot leave the renderer silently behind.
   */
  actor?: Actor
  /**
   * What this Step is FOR in an agentic flow: the decision point, a capability, the human gate.
   *
   * A separate channel from `actor`, because they answer different questions and a Step has both: the human
   * gate is `actor: 'external'` (a person must publish) AND `agentRole: 'human-gate'` (it is where the agent
   * hands over). Collapsing them would mean either losing the accent that says who must act or losing the
   * silhouette that says this is a decision rather than work.
   */
  agentRole?: AgentRole
  /** Status as TEXT, not colour alone — Temporal had to retrofit exactly this. */
  status?: string
  /**
   * The two-cell phase token. Replaces a single rolled-up status, because our two phases are separate
   * commits with independent failure — one token would have to lie about one of them.
   */
  token?: CardToken
  /**
   * ONE line of templated prose, rendered only when blocked or failed. This is the answer to "the run
   * decoration is too sparse": the hard question is never *what* state but *why*.
   */
  reason?: Reason
  rows?: BoxRow[]
  badge?: string
  /**
   * Which way a non-rectangular silhouette points, for kinds that have one (`rpc`).
   *
   * The view sets it because only the view knows the reading direction: the node is aimed AT the step
   * it feeds, so the point follows `direction` rather than being baked into the stylesheet.
   */
  point?: 'right' | 'down'
  /**
   * BPMN's LOOP MARKER: this step can transition to itself.
   *
   * Drawn on the card as well as as an edge, because the arc is only a few pixels at a small zoom and
   * "this repeats" is too important to depend on tracing it. BPMN puts the same glyph at the bottom of
   * an activity for exactly this reason — the marker states the behaviour, the edge shows the path.
   */
  loops?: boolean
  /**
   * A second mark, for a Step that is also a shared failure target.
   *
   * Separate from `badge` because the two facts are independent and a Step can be both: the book
   * pipeline's RecoveryGate is a human gate AND the dispatcher every failure routes to, and while the
   * badge slot held only one of them it said "needs you" and never mentioned the other.
   */
  recovery?: { glyph: string; title: string }
  emphasis?: 'start' | 'hub' | 'muted' | 'active' | 'failed' | 'planned'
  sections?: { label: string; rows: BoxRow[] }[]
  /**
   * The proportional state bar for a StepType that ran more than once.
   *
   * Airflow's mechanic: each segment's width IS the count, with a 2px floor so one failure among a
   * thousand successes stays visible. It is what makes one-card-per-StepType survive a loop.
   */
  bar?: { status: PhaseStatus; count: number }[]
  /** The bar's hover decode: a count per status plus the wait-kind breakdown. */
  barTitle?: string
}

/**
 * The closed set of edge meanings. A value here, rather than a bare union, so a test can iterate them
 * and prove each one has an arrowhead — the gap that let every edge ship with no direction at all.
 */
export const FAMILIES = [
  'control',
  'failure',
  'resource',
  'wait',
  'subflow',
  'gate',
  'rpc',
] as const

export interface Link {
  id: string
  from: string
  to: string
  /**
   * Solid for control, dashed for everything else — one pre-attentive channel.
   *
   * `rpc` is BPMN's Message Flow, transplanted: the connector from something OUTSIDE the process to
   * the step it delivers to. BPMN reserves a distinct connector for exactly this (dashed, hollow
   * arrowhead, hollow circle at the source) and forbids ordinary Sequence Flow from crossing a pool
   * boundary at all. We keep that separation because the meanings differ in kind: a control edge says
   * "the flow proceeds here", an rpc edge says "the outside world reached in here".
   */
  family: (typeof FAMILIES)[number]
  label?: string
  /**
   * This edge was TRAVERSED by the run, and when — 1 for the first move, 2 for the second.
   *
   * Drives the live execution map's strongest signal. It also OVERRIDES latency: a hub's inbound edges are
   * normally quiet because eight of them at once is a star nobody can trace, but an edge the run actually
   * took is never noise, so the path stays drawn while everything it did not take stays quiet. That
   * inversion is what makes a wide agentic definition readable during a run.
   */
  onPath?: number
  /** Only drawn when the link is lit by selection. Raw guards live here, not on cards. */
  detail?: string
  selfLoop?: boolean
  /**
   * `side` = do not run straight through the column; leave by a side face and come back.
   *
   * Set by the view once positions are known, because only the view can tell that an edge SKIPS a row
   * and would therefore pass underneath the cards in between. Failure edges have always been routed
   * this way; this generalises it to any edge that would tunnel through a step it has nothing to do
   * with.
   */
  route?: 'side' | 'around'
  /** Latent links exist but stay quiet until their endpoint is selected. */
  latent?: boolean
}

/** A background region: a swimlane, a container frame, a timeline gutter. */
export interface Band {
  id: string
  x: number
  y: number
  w: number
  h: number
  label?: string
  /** 'lane' tints; 'frame' outlines; 'group' is n8n's labelled region. */
  style: 'lane' | 'frame' | 'group'
  /** `rpc` is the outside-the-flow lane — BPMN's collapsed pool for an external participant. */
  tone?: Actor | 'neutral' | 'rpc'
  /** Cycles the group tint, so adjacent regions are distinguishable without meaning anything. */
  hue?: number
}

export interface Scene {
  boxes: Box[]
  links: Link[]
  bands: Band[]
  width: number
  height: number
  /** Shown under the stage. Use it to admit what this view cannot show. */
  notes: string[]
}

export interface ViewSpec {
  id: string
  letter: string
  label: string
  /** The Dex fact this view is derived from. Shown in the UI so the claim travels with it. */
  principle: string
  claim: string
  risk: string
  layout: (flow: PocFlow, opts: ViewOpts) => Scene
}

export const emptyScene = (note: string): Scene => ({
  boxes: [],
  links: [],
  bands: [],
  width: 640,
  height: 200,
  notes: [note],
})

export function boundsOf(boxes: Box[], bands: Band[], pad = 48): { width: number; height: number } {
  let maxX = 0
  let maxY = 0
  for (const b of [...boxes, ...bands]) {
    maxX = Math.max(maxX, b.x + b.w)
    maxY = Math.max(maxY, b.y + b.h)
  }
  return { width: maxX + pad, height: maxY + pad }
}
