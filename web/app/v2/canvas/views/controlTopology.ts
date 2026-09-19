// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import dagre from 'dagre'
import type { PocFlow } from '../model/pocFlow'
import {
  controlLinks,
  coverageNotes,
  externalRpcBoxes,
  externalRpcs,
  gateLinks,
  ORIGIN_W,
  originBoxes,
  rpcCoverageNote,
  subflowBoxes,
} from './shared'
import { STEP_W, stepBox, stepContent } from './stepBox'
import type { Band, Box, Scene, ViewOpts, ViewSpec } from './types'
import { boundsOf } from './types'

/**
 * A · Control Topology — the base view.
 *
 * Only Steps reach the layout engine, and only control edges. In the reference corpus that is 89 of
 * 468 nodes and 122 of 335 edges; everything else is placed by arithmetic relative to the result.
 * A general layout over all kinds is the hairball.
 */

const ORIGIN_X = 20
const COL_X = ORIGIN_X + ORIGIN_W + 44
const PER_ROW = 10
/**
 * A pathological-width safety valve, not a layout choice.
 *
 * Wrapping used to fire at 4, and it was the single largest source of edge crossings in the corpus:
 * splitting one rank across two rows turns every edge into the second row into a rank-skipping edge.
 * Measured on the real Go flow, wrapping at 4 gave NINE crossings where not wrapping gives ONE — and
 * "never introduce a crossing to make the diagram more compact" is the rule it was breaking. The cost
 * of not wrapping is a wider scene (1,933px against 1,377px) and a shorter one (548px against 662px),
 * which fit-to-view absorbs.
 */
const GROUP_PAD = 12
// Narrower across the columns than along the ranks: the column gap is 34px, so two adjacent
// groups' bands would touch at 14 and read as one region.
const GROUP_PAD_X = 11
/**
 * Across-axis room an origin or RPC pennant needs before the first row.
 *
 * Must clear the first band's LABEL HEADROOM, not just the first card: a band reaches
 * GROUP_PAD_X + GROUP_LABEL_H past its topmost member, and the pennant sits in exactly that space
 * left-right. So this is the pennant's own across-extent (RPC_H + 14) plus that reach, plus slack.
 */
const RPC_GUTTER = 100
const GROUP_LABEL_H = 18

/**
 * Ranks a CYCLIC control graph: cycle removal, then longest-path layering.
 *
 * This is Sugiyama's first two phases done properly, and the third attempt at it. What came before and
 * why each failed, because the failures are instructive:
 *
 *  1. IN-DEGREE PEELING. A Dex control graph is usually a loop, not a DAG — the real agent flow has no
 *     Step with in-degree zero except its start — so peeling settled nothing and drew the whole flow as
 *     one flat row.
 *  2. BFS DEPTH AS THE FORWARD TEST. Better, but it treats "reachable in N hops from the start" as the
 *     layer, and a hub that fans back out short-circuits that. The real book pipeline exposed it: a
 *     RecoveryGate catching eight failures also transitions back to four different Steps, which pulled
 *     their BFS depths level. A genuine SEVEN-STEP CHAIN then had every member at the same depth, so
 *     every link in it counted as same-layer, was excluded from the longest-path pass, and the chain
 *     collapsed into one row of seven boxes joined by six sideways edges. 36 crossings over 39 edges.
 *
 * So: classify edges by DFS instead. An edge is a BACK EDGE exactly when its target is on the current
 * DFS stack — the textbook definition, and unlike a depth comparison it cannot be fooled by a shortcut.
 * Removing those leaves a DAG by construction, and longest-path over the DAG gives each Step a rank one
 * below its latest producer.
 */
function cyclicRanks(
  ids: string[],
  edges: { from: string; to: string }[],
  startId: string | undefined,
): Map<string, number> {
  const real = edges.filter((e) => e.from !== e.to)
  const out = new Map<string, string[]>()
  for (const e of real) {
    out.set(e.from, [...(out.get(e.from) ?? []), e.to])
  }

  /**
   * Phase 1 — cycle removal. Depth-first, starting at the Flow's start Step so the spanning tree
   * follows the direction a reader travels, then at anything still unvisited so a Step reachable only
   * by failing still gets classified.
   */
  const onStack = new Set<string>()
  const seen = new Set<string>()
  const backEdges = new Set<string>()
  const key = (a: string, b: string): string => `${a}\u0000${b}`
  const visit = (v: string): void => {
    seen.add(v)
    onStack.add(v)
    for (const w of out.get(v) ?? []) {
      if (onStack.has(w)) {
        backEdges.add(key(v, w))
        continue
      }
      if (!seen.has(w)) visit(w)
    }
    onStack.delete(v)
  }
  const seed = startId !== undefined && ids.includes(startId) ? startId : ids[0]
  if (seed !== undefined) visit(seed)
  for (const id of ids) if (!seen.has(id)) visit(id)

  const forward = real.filter((e) => !backEdges.has(key(e.from, e.to)))

  /**
   * Phase 2 — longest path over the DAG. Iterative relaxation rather than a topological sort, because
   * it needs no second ordering pass and converges in at most `ids.length` rounds on a DAG.
   */
  const rank = new Map<string, number>(ids.map((id) => [id, 0]))
  for (let round = 0; round < ids.length; round++) {
    let moved = false
    for (const e of forward) {
      const want = (rank.get(e.from) ?? 0) + 1
      if (want > (rank.get(e.to) ?? 0)) {
        rank.set(e.to, want)
        moved = true
      }
    }
    if (!moved) break
  }
  return rank
}

/**
 * Steps reachable ONLY by failing into them.
 *
 * These go in a right-hand gutter rather than the main column, which is Zapier's answer: error
 * handling is a permanent path with a fixed position (`Success` left, `Error` right) so a reader
 * gets the distinction from geometry without a legend. It also stops recovery edges reaching
 * backwards across the happy path, which was the actual complaint that made recovery a toggle.
 */
function recoverySteps(flow: PocFlow): Set<string> {
  const start = flow.startStepId ?? flow.steps[0]?.id
  const reachable = new Set<string>()
  if (start !== undefined) {
    const adj = new Map<string, string[]>()
    for (const t of flow.transitions) {
      if (t.kind !== 'transition') continue
      const l = adj.get(t.fromStepId) ?? []
      l.push(t.toStepId)
      adj.set(t.fromStepId, l)
    }
    const q = [start]
    reachable.add(start)
    while (q.length > 0) {
      const id = q.shift() as string
      for (const n of adj.get(id) ?? []) {
        if (reachable.has(n)) continue
        reachable.add(n)
        q.push(n)
      }
    }
  }
  /**
   * A RECOVERY HUB goes aside too, not just a Step you can only reach by failing.
   *
   * The book pipeline's RecoveryGate is reachable by an ordinary `transition`, so reachability alone
   * left it on the main spine — in the middle of a twelve-step chain, as though every volume routinely
   * passes through recovery. It does not. Every one of its 22 inbound edges is an error path: 11 are
   * `on_execute_failure_proceed_to`, and the rest are guarded `go_to(RecoveryGate)` branches that each
   * set a StageFailure first. `ApproveItemsStep`'s, for instance, fires only on "no approved beat plans
   * — nothing to translate"; its happy path goes to `ProduceWaveStep`.
   *
   * `isRecoveryHub` is already the measured "catches two or more failures", so it is the right test.
   * Ordinary saga structure is unaffected: money-transfer's CompensateStep was already aside.
   */
  const unreachable = flow.steps.filter((s) => !reachable.has(s.id)).map((s) => s.id)
  // A SHARED failure target — sink or dispatcher — leaves the spine. A `fallback` (exactly one Step's
  // Plan B) does not need to: it has one feeder, so drawn inline its single edge is already short.
  const hubs = flow.steps
    .filter((s) => s.recoveryRole === 'sink' || s.recoveryRole === 'dispatcher')
    .map((s) => s.id)
  return new Set([...unreachable, ...hubs])
}

function layout(flow: PocFlow, opts: ViewOpts): Scene {
  if (flow.steps.length === 0) {
    return { boxes: [], links: [], bands: [], width: 640, height: 200, notes: ['No steps.'] }
  }

  const aside = recoverySteps(flow)
  const main = flow.steps.filter((s) => !aside.has(s.id))
  const links = controlLinks(flow, opts)
  const control = links.filter((l) => l.family === 'control')

  const ids = main.map((s) => s.id)
  const rank = cyclicRanks(
    ids,
    control.filter((l) => ids.includes(l.from) && ids.includes(l.to)),
    flow.startStepId,
  )

  // The start Step is forced to rank 0. A process view whose first box is not the beginning is
  // actively misleading, and with back-edges present ranking does not reliably put it first.
  if (flow.startStepId !== undefined && rank.has(flow.startStepId)) {
    if ((rank.get(flow.startStepId) ?? 0) !== 0) {
      for (const [id, r] of rank) rank.set(id, id === flow.startStepId ? 0 : r + 1)
    }
  }

  // dagre supplies within-rank ORDER only — a crossing-reduction heuristic, nothing more.
  const g = new dagre.graphlib.Graph()
  g.setGraph({ rankdir: opts.direction === 'lr' ? 'LR' : 'TB', nodesep: 36, ranksep: 72 })
  g.setDefaultEdgeLabel(() => ({}))
  const heights = new Map<string, number>()
  for (const s of flow.steps) {
    const h = stepContent(flow, s, opts).height
    heights.set(s.id, h)
    if (!aside.has(s.id)) g.setNode(s.id, { width: STEP_W, height: h })
  }
  for (const l of control) {
    if (l.from !== l.to && !aside.has(l.from) && !aside.has(l.to)) g.setEdge(l.from, l.to)
  }
  try {
    dagre.layout(g)
  } catch {
    // A malformed graph must not blank the stage; alphabetical order is a fine fallback.
  }

  const byRank = new Map<number, string[]>()
  for (const s of main) {
    const r = rank.get(s.id) ?? 0
    const list = byRank.get(r) ?? []
    list.push(s.id)
    byRank.set(r, list)
  }
  /**
   * Group membership, as a rank-order key.
   *
   * This is the half of grouping that actually reduces overlap: members of a group are sorted
   * ADJACENT within their rank, so a band can enclose them without swallowing an unrelated step or
   * splitting into a disjoint region. Without it the bands would be correct rectangles around
   * scattered members, which is worse than no bands.
   *
   * Group order beats dagre's crossing hint, which is a deliberate trade: a few more edge crossings
   * in exchange for regions that are contiguous and therefore labelable.
   */
  const groupOf = new Map<string, number>()
  ;(opts.groups ?? []).forEach((grp, i) => {
    for (const t of grp.stepTypes) {
      const step = flow.steps.find((x) => x.stepType === t)
      if (step !== undefined) groupOf.set(step.id, i)
    }
  })

  /**
   * TRIED AND REJECTED, on measurement: ordering a group's members by the order the AUTHOR declared
   * them in.
   *
   * It is tempting — `Tool use` is declared RouteTool -> AwaitToolApproval -> ExecuteTool ("choose a
   * tool, get it approved, run it"), and the crossing-reduction heuristic instead produces
   * AwaitToolApproval, ExecuteTool, RouteTool, which reads like nothing. But imposing declared order
   * inside the group fights the heuristic across the row, and the aesthetics gate measured the cost
   * immediately: crossings on the real flow went from 1 to 3.
   *
   * Crossings are the aesthetic with the strongest claim on reading comprehension, so a nicer
   * within-group order does not buy them. Left here so the idea is not re-tried blind.
   */

  for (const list of byRank.values()) {
    // dagre's CROSS-axis coordinate is the crossing-reduction hint, and which coordinate that is
    // swaps with the direction.
    const cross = (id: string): number => {
      const n = g.node(id) as { x?: number; y?: number } | undefined
      return (opts.direction === 'lr' ? n?.y : n?.x) ?? 0
    }
    const grp = (id: string): number => groupOf.get(id) ?? Number.MAX_SAFE_INTEGER
    list.sort(
      (a, b) =>
        grp(a) - grp(b) || (cross(a) === cross(b) ? a.localeCompare(b) : cross(a) - cross(b)),
    )
  }

  /**
   * Rank separation follows whether ANY Step in this graph has anatomy — a structural fact, not a
   * zoom one. A gap tuned for tall cards turns a collapsed graph into a sparse ribbon whose
   * bounding box forces fit-to-view to zoom the text away.
   */
  /**
   * Does any card actually have ANATOMY — rows or sections — as opposed to merely being tall?
   *
   * This used to be `height > 66`, and a run breaks that: a card with no anatomy at all grows to 71px
   * the moment it gains a 22px reason strip, so a COLLAPSED graph silently inherited expanded rank
   * spacing. Measured on money-transfer/collapsed, adding a run moved the gaps from 72,30,72,30 to
   * 79,56,98,56 — which also destroys the within-group-closer-than-cross-group rhythm.
   *
   * Asking the content directly cannot drift the way a pixel threshold does.
   */
  const anyAnatomy = flow.steps.some((st) => {
    const c = stepContent(flow, st, opts)
    return c.rows.length > 0 || c.sections.length > 0
  })
  /**
   * The base gap between rows, before any group band asks for room. `bandAllowance` adds the rest, per
   * boundary, further down — two earlier attempts got that wrong in opposite directions:
   *
   *  1. Widening only where a whole RANK changed group never fired on a fan-out, because a four-wide
   *     rank holding two groups reads as "mixed"; bands then overlapped by exactly two paddings.
   *  2. Widening EVERY gap by the full allowance fixed the overlap but charged gaps inside a single
   *     group for band edges they do not have, so a step sat as far from its own group partner as from
   *     an unrelated step.
   */
  const rankBase = anyAnatomy ? 56 : 30
  /**
   * A WRAPPED continuation needs the allowance too.
   *
   * This is where the first fix leaked. A wide rank wraps at four per row, so the agent flow's
   * six-member rank becomes two rows 20px apart — and 20px is a rank-internal gap, not a rank gap, so
   * it never got the allowance. `Tool use` and `Backoff` sit on that wrapped row, and their bands
   * overlapped `Model call` by 4px. Found by the overlap test, not by looking: 4px of translucent
   * background is invisible.
   */
  const wrapBase = 20
  const lr = opts.direction === 'lr'

  const boxes: Box[] = []
  const posOfStep = new Map<string, { x: number; y: number }>()

  /**
   * Rows, in reading order. A rank normally IS a row; a pathologically wide one wraps into several.
   * Flattened first so the coordinate pass below can treat "adjacent row" uniformly.
   */
  const rowsWithRank: { rank: number; ids: string[] }[] = []
  for (const r of [...byRank.keys()].sort((a, b) => a - b)) {
    const inRank = byRank.get(r) ?? []
    for (let start = 0; start < inRank.length; start += PER_ROW) {
      rowsWithRank.push({ rank: r, ids: inRank.slice(start, start + PER_ROW) })
    }
  }
  const rows = rowsWithRank.map((r) => r.ids)
  const rowIndexOf = new Map<string, number>()
  rows.forEach((row, i) => row.forEach((id) => rowIndexOf.set(id, i)))

  /**
   * COORDINATE ASSIGNMENT — Sugiyama's last phase, which this layout was missing.
   *
   * Positions used to be the node's INDEX in its row: `COL_X + i * SLOT`. That puts every row hard
   * against the left margin, so a parent with six children sat at the far-left of its own fan and five
   * of six edges bent rightward — a lopsided rake instead of a symmetric spread. It is also the reason
   * the picture read as "arrows everywhere" rather than as one bus.
   *
   * The fix is the standard median/priority heuristic: sweep the rows, put each node at the MEDIAN of
   * its neighbours in the adjacent row, then enforce the established order and a minimum separation.
   * Brandes-Köpf is the linear-time version of this with a two-bends-per-edge guarantee; this is the
   * simple iterative form, which is plenty at our sizes and keeps the ordering the crossing-reduction
   * pass produced.
   *
   * Sweeps alternate direction because the two ends want different things: aligning children under
   * parents straightens chains, aligning parents over children centres fans. Doing both, repeatedly,
   * settles.
   */
  /**
   * Separation is each node's OWN across-extent plus a fixed gap, not one global slot.
   *
   * The global slot was `max(height across the whole flow) + 26 + 34` in `lr`, so every neighbour pair
   * in every column was spaced for the tallest card anywhere. Measured on ai-agent/expanded/lr, the
   * six-card column's visible gaps came out 202, 202, 162, 202, 185 where top-down draws the same
   * adjacency at a flat 34. Uniform edge length is one of the standard quality measures, and one global
   * slot is the opposite of it once extents vary.
   *
   * Top-down every card is STEP_W wide, so extent-plus-gap reduces to exactly the old behaviour there.
   */
  const GAP_ACROSS = 34
  const extentAcross = (id: string): number => (lr ? (heights.get(id) ?? 52) : STEP_W)

  /**
   * WHICH AXIS CARRIES THE LABEL STRIP. `.pband-label` is `position:absolute; top:6px`, so a band's
   * name always sits on its y-minimum edge — which is the ALONG axis top-down and the ACROSS axis
   * left-right. Everything about reserving room for it has to follow that, and previously none of it
   * did: the strip was carved out of `y` in both directions while the space for it was reserved on
   * `along`, so every left-right band ate 30px of whatever sat above it.
   */
  const labelOnAcross = lr

  /** Adjacent members of one row need room for two band edges when they belong to different groups. */
  const sep = (leftId: string, rightId?: string): number => {
    const bare = extentAcross(leftId) + GAP_ACROSS
    if (rightId === undefined) return bare
    const a = groupOf.get(leftId)
    const b = groupOf.get(rightId)
    if (a === b) return bare
    const need =
      extentAcross(leftId) + GROUP_PAD_X * 2 + (labelOnAcross ? GROUP_LABEL_H : 0)
    return Math.max(bare, need)
  }
  const adj = { down: new Map<string, string[]>(), up: new Map<string, string[]>() }
  for (const l of control) {
    if (l.from === l.to) continue
    const a = rowIndexOf.get(l.from)
    const b = rowIndexOf.get(l.to)
    if (a === undefined || b === undefined || a === b) continue
    adj.down.set(l.to, [...(adj.down.get(l.to) ?? []), l.from])
    adj.up.set(l.from, [...(adj.up.get(l.from) ?? []), l.to])
  }

  const across = new Map<string, number>()
  for (const row of rows) {
    let at = 0
    row.forEach((id, i) => {
      across.set(id, at)
      at += sep(id, row[i + 1])
    })
  }

  const median = (vals: number[]): number | undefined => {
    if (vals.length === 0) return undefined
    const v = [...vals].sort((a, b) => a - b)
    const m = v.length >> 1
    return v.length % 2 === 1 ? (v[m] as number) : ((v[m - 1] as number) + (v[m] as number)) / 2
  }

  for (let sweep = 0; sweep < 6; sweep++) {
    // Even sweeps pull children under parents; odd sweeps pull parents over children.
    const useParents = sweep % 2 === 0
    const order = useParents ? rows : [...rows].reverse()
    for (const row of order) {
      const want = row.map((id) => {
        const near = (useParents ? adj.down.get(id) : adj.up.get(id)) ?? []
        const xs = near.map((n) => across.get(n)).filter((v): v is number => v !== undefined)
        return median(xs) ?? (across.get(id) as number)
      })
      // Left-to-right: honour the desired position, but never break the row's order or overlap.
      const placed = [...want]
      for (let i = 1; i < placed.length; i++) {
        const prev = row[i - 1] as string
        placed[i] = Math.max(placed[i] as number, (placed[i - 1] as number) + sep(prev, row[i]))
      }
      // Right-to-left relaxation, so satisfying one node does not drag the whole row rightward.
      for (let i = placed.length - 2; i >= 0; i--) {
        placed[i] = Math.min(
          placed[i] as number,
          (placed[i + 1] as number) - sep(row[i] as string, row[i + 1]),
        )
      }
      row.forEach((id, i) => across.set(id, placed[i] as number))
    }
  }

  // Normalise to the gutter: the sweeps work in relative space and can go negative.
  const minAcross = Math.min(...across.values())
  /**
   * The across-axis origin, reserving the same things in both directions.
   *
   * Top-down, `COL_X` already bakes in the origin/RPC gutter (20 + ORIGIN_W + 44). Left-right used a
   * bare 34 and reserved nothing, so the pennant — placed BEFORE the first row on the across axis —
   * landed at negative y, outside the scene's own bounds, in eight of sixteen configurations. And when
   * the label strip is on the across axis it needs its headroom here too.
   */
  const needsRpcGutter = externalRpcs(flow).length > 0 || flow.entries.length > 0
  const opensAtAcrossMin = (opts.groups ?? []).length > 0
  const base = lr
    ? 34 +
      (needsRpcGutter ? RPC_GUTTER : 0) +
      (labelOnAcross && opensAtAcrossMin ? GROUP_LABEL_H + GROUP_PAD_X : 0)
    : COL_X
  for (const [id, v] of across) across.set(id, v - minAcross + base)

  // `along` advances with the row; `across` is the within-row offset. Swapping which axis each maps
  // to is the whole of the direction setting.
  /**
   * Where content starts on the reading axis.
   *
   * Shared by the main column and the recovery gutter, because they are two columns of the same
   * drawing and a reader reads across them. It used to be inlined in one place and a bare `34` in the
   * other, so once groups reserved label headroom the gutter kept the old origin and the recovery step
   * floated 30px ABOVE the first step — a start-of-flow that is not at the start.
   */
  /**
   * The band allowance a given row BOUNDARY actually needs, rather than a flat surcharge everywhere.
   *
   * The flat version was wrong in a way only the eye caught: every gap paid the full 42px for a band's
   * bottom padding, the next band's top padding and its label strip, INCLUDING gaps inside a single
   * group where none of those three things exist. So `CreateDebitMemoStep` sat as far from `DebitStep`
   * — its own group partner — as it did from a step in a different group, and because a within-group
   * gap is unbroken tint while a cross-group gap is chunked by two band edges and a strip of
   * background, the equal distance read as LARGER within the group. Exactly backwards: semantic
   * proximity is supposed to show up as spatial proximity.
   *
   * So the surcharge is now charged per boundary, for the parts that boundary really has:
   *   - a group ENDING at this row wants its bottom padding,
   *   - a group STARTING at the next row wants its top padding, plus a label strip if that is the
   *     first region it has anywhere.
   * A group that simply continues across the boundary has no edge there and pays nothing.
   */
  const groupsIn = (row: string[]): Set<number> => {
    const out = new Set<number>()
    for (const id of row) {
      const g = groupOf.get(id)
      if (g !== undefined) out.add(g)
    }
    return out
  }
  const firstRowOfGroup = new Map<number, number>()
  rows.forEach((row, i) => {
    for (const g of groupsIn(row)) if (!firstRowOfGroup.has(g)) firstRowOfGroup.set(g, i)
  })
  const bandAllowance = (i: number): number => {
    const here = groupsIn(rows[i] ?? [])
    const next = groupsIn(rows[i + 1] ?? [])
    const ends = [...here].some((g) => !next.has(g))
    const starting = [...next].filter((g) => !here.has(g))
    const labels = starting.some((g) => firstRowOfGroup.get(g) === i + 1)
    return (
      (ends ? GROUP_PAD : 0) +
      (starting.length > 0 ? GROUP_PAD : 0) +
      (labels && !labelOnAcross ? GROUP_LABEL_H : 0)
    )
  }

  // The first row only needs headroom if a band actually opens there with a label to print.
  const opensAtTop = [...groupsIn(rows[0] ?? [])].some((g) => firstRowOfGroup.get(g) === 0)
  const contentStart = 34 + (opensAtTop && !labelOnAcross ? GROUP_LABEL_H + GROUP_PAD : 0)
  let along = contentStart
  let widestRow = 1
  rows.forEach((row, i) => {
    widestRow = Math.max(widestRow, row.length)
    const thickness = lr ? STEP_W : Math.max(...row.map((id) => heights.get(id) ?? 52))
    for (const id of row) {
      const step = flow.steps.find((s) => s.id === id)
      if (step === undefined) continue
      const a = across.get(id) ?? base
      const pos = lr ? { x: along, y: a } : { x: a, y: along }
      boxes.push(stepBox(flow, step, opts, pos.x, pos.y))
      posOfStep.set(id, pos)
    }
    // A wrapped continuation sits closer than a genuine rank change, so a wrap reads as one fan-out
    // rather than as two stages.
    const next = rowsWithRank[i + 1]
    const sameRank = next !== undefined && next.rank === rowsWithRank[i]?.rank
    along += thickness + (sameRank ? wrapBase : rankBase) + bandAllowance(i)
  })

  /**
   * The failure gutter. Always on the cross axis's far side, which is Zapier's answer: error handling
   * has a fixed position (`Success` left, `Error` right) so the distinction comes from geometry
   * rather than a legend, and recovery edges stop reaching back across the happy path.
   */
  /**
   * The far edge of the main content on the across axis, measured from what is actually placed.
   *
   * Both branches were wrong in their own way: top-down multiplied a column index by a slot width,
   * which stopped being true once coordinates came from the median sweep; left-right added a bare 120
   * to the maximum TOP edge, which is height-blind, so at 'expanded' the recovery gutter slid back into
   * the main column and its band landed on top of a card.
   */
  const contentFar = Math.max(
    ...[...posOfStep.entries()].map(([id, p]) => (lr ? p.y : p.x) + extentAcross(id)),
    base,
  )
  const spanAcross = contentFar + GAP_ACROSS + GROUP_PAD * 2
  /**
   * The gutter aligns each aside Step to the MIDDLE of what feeds it, then falls back to stacking.
   *
   * Stacking from the top is what made an earlier attempt at this worse rather than better: the
   * recovery hub went to row 0 while its edges came from every row of the flow, so all 22 of them
   * reached diagonally down the gutter. Aligned to the mean of its sources, the same edges are short and
   * roughly symmetric.
   */
  const alongOfPos = (id: string): number | undefined => {
    const p = posOfStep.get(id)
    return p === undefined ? undefined : lr ? p.x : p.y
  }
  const feedersMid = (stepId: string): number | undefined => {
    const froms = flow.transitions
      .filter((t) => t.toStepId === stepId && t.fromStepId !== stepId)
      .map((t) => alongOfPos(t.fromStepId))
      .filter((v): v is number => v !== undefined)
    if (froms.length === 0) return undefined
    return froms.reduce((a, b) => a + b, 0) / froms.length
  }

  const asideIds = flow.steps.filter((s) => aside.has(s.id)).map((s) => s.id)
  const asidePlaced: { id: string; at: number; extent: number }[] = []
  for (const id of asideIds) {
    const extent = lr ? STEP_W : (heights.get(id) ?? 52)
    const want = feedersMid(id) ?? contentStart
    let at = Math.max(contentStart, want - extent / 2)
    // Never overlap a neighbour already in the gutter.
    for (const prior of asidePlaced) {
      if (at < prior.at + prior.extent + rankBase && at + extent + rankBase > prior.at) {
        at = prior.at + prior.extent + rankBase
      }
    }
    asidePlaced.push({ id, at, extent })
    const step = flow.steps.find((x) => x.id === id)
    if (step === undefined) continue
    const pos = lr ? { x: at, y: spanAcross } : { x: spanAcross, y: at }
    boxes.push(stepBox(flow, step, opts, pos.x, pos.y))
    posOfStep.set(id, pos)
  }

  // Origins sit before the flow on the reading axis; subflows after it on the cross axis.
  const originAt = (id: string): number | undefined =>
    lr ? posOfStep.get(id)?.x : posOfStep.get(id)?.y
  boxes.push(
    ...originBoxes(flow, ORIGIN_X, originAt).map((b) =>
      lr ? { ...b, x: b.y, y: 34 - 60 } : b,
    ),
  )

  /**
   * External RPCs share the origin gutter, aimed at the step they unblock.
   *
   * The same side as an origin on purpose: both are the outside of the system reaching in, and a
   * reader who learns "left edge means it came from outside" gets both for one rule. This is Zapier's
   * geometry-as-semantics applied to entry rather than to error.
   */
  // Centre, not top edge: a 46px pennant top-aligned to a taller card puts a kink in a straight run.
  const centreAt = (id: string): number | undefined => {
    const p = posOfStep.get(id)
    if (p === undefined) return undefined
    return lr ? p.x + STEP_W / 2 : p.y + (heights.get(id) ?? 52) / 2
  }
  boxes.push(
    ...externalRpcBoxes(flow, lr ? base - RPC_GUTTER : ORIGIN_X, centreAt, lr),
  )
  // Past the recovery gutter when there is one, by that gutter's own across-extent.
  const asideExtent = Math.max(
    0,
    ...flow.steps.filter((st) => aside.has(st.id)).map((st) => extentAcross(st.id)),
  )
  const subAcross = spanAcross + (aside.size > 0 ? asideExtent + GAP_ACROSS : 0)
  boxes.push(
    ...subflowBoxes(flow, subAcross, originAt).map((b) => (lr ? { ...b, x: b.y, y: subAcross } : b)),
  )

  /**
   * The bands, as the bounding box of each group's placed members plus padding.
   *
   * Computed AFTER layout rather than reserved before it, so a group never pushes the graph around —
   * it describes where its members ended up. A group whose members did not all get placed still gets
   * a band around the ones that did, because a partial region is still true.
   */
  const bands: Band[] = []
  ;(opts.groups ?? []).forEach((grp, i) => {
    const members = grp.stepTypes
      .map((t) => flow.steps.find((x) => x.stepType === t)?.id)
      .filter((id): id is string => id !== undefined && posOfStep.has(id))
    if (members.length === 0) return

    /**
     * ONE CONTIGUOUS REGION where that is possible, split only where it is not.
     *
     * A group wants to be a single area covering the gap between its members — "Debit side" reads as
     * one region, not as two squares with a corridor between them. So start from per-row rectangles
     * and MERGE adjacent ones, keeping a merge only when the union encloses no step the group does not
     * own.
     *
     * That test is the whole point. A plain bounding box per group is wrong in a cyclic flow: `Tool
     * use` has two members in the agent flow's four-wide fan and a third on the wrapped row below, so
     * its box spanned every column and swallowed `Model call` and `Backoff`, stacking three labels on
     * one pixel. Merging greedily but rejecting any union that captures a foreign card gives
     * money-transfer one tall band per side AND keeps the agent flow's regions honest, without either
     * case needing to be special.
     */
    const owned = new Set(members)
    // Any box, not just steps: a merged band that swallowed an RPC pennant or a subflow would be just
    // as false a claim as one that swallowed a step.
    const foreign = boxes.filter((b) => !owned.has(b.id))
    const swallows = (r: { x: number; y: number; w: number; h: number }): boolean =>
      foreign.some(
        (b) =>
          b.x + b.w > r.x && b.x < r.x + r.w && b.y + b.h > r.y && b.y < r.y + r.h,
      )

    const rows = new Map<number, { x: number; y: number; h: number }[]>()
    for (const id of members) {
      const p = posOfStep.get(id) as { x: number; y: number }
      const key = lr ? p.x : p.y
      rows.set(key, [...(rows.get(key) ?? []), { x: p.x, y: p.y, h: heights.get(id) ?? 52 }])
    }
    let rects = [...rows.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([, rs]) => {
        const x0 = Math.min(...rs.map((r) => r.x))
        const y0 = Math.min(...rs.map((r) => r.y))
        return {
          x: x0,
          y: y0,
          w: Math.max(...rs.map((r) => r.x + STEP_W)) - x0,
          h: Math.max(...rs.map((r) => r.y + r.h)) - y0,
        }
      })

    // Greedy pairwise merge until nothing more can join without capturing a foreign card.
    for (let pass = 0; pass < rects.length; pass++) {
      let merged = false
      for (let a = 0; a < rects.length - 1 && !merged; a++) {
        const p = rects[a] as { x: number; y: number; w: number; h: number }
        const q = rects[a + 1] as { x: number; y: number; w: number; h: number }
        const x0 = Math.min(p.x, q.x)
        const y0 = Math.min(p.y, q.y)
        const union = {
          x: x0,
          y: y0,
          w: Math.max(p.x + p.w, q.x + q.w) - x0,
          h: Math.max(p.y + p.h, q.y + q.h) - y0,
        }
        if (swallows(union)) continue
        rects = [...rects.slice(0, a), union, ...rects.slice(a + 2)]
        merged = true
      }
      if (!merged) break
    }

    rects.forEach((r, rectIndex) => {
      // Narrow pad on the across axis (its gap is only GAP_ACROSS), normal pad along the ranks. The
      // label strip is always on the y-minimum edge, because that is where the stylesheet draws it.
      const padX = lr ? GROUP_PAD : GROUP_PAD_X
      const padY = lr ? GROUP_PAD_X : GROUP_PAD
      const strip = rectIndex === 0 ? GROUP_LABEL_H : 0
      bands.push({
        id: `${grp.id}:${Math.round(lr ? r.x : r.y)}`,
        x: r.x - padX,
        y: r.y - padY - strip,
        w: r.w + padX * 2,
        h: r.h + padY * 2 + strip,
        label: rectIndex === 0 ? grp.label : undefined,
        style: 'group',
        hue: i,
      })
    })
  })

  /**
   * ROW-SKIPPING GUARDRAIL.
   *
   * An edge whose endpoints are more than one row apart passes straight through every card in between,
   * which reads as an arrow tunnelling under a step it has no relationship with. Measured: only the
   * agent flow has any — 2 of them, both caused by its six-member rank wrapping into two rows, so
   * `CheckSteered` reaches a row that is two down. They are sent out a side face instead.
   *
   * Done after placement because it is a question about geometry, not about the graph: the same two
   * steps are adjacent or not depending on how the rank wrapped.
   */
  // Buckets on the ALONG axis, which is y top-down and x left-right. Reading `b.y` unconditionally
  // measured the wrong axis in `lr` and mis-routed three of the agent flow's edges aside.
  const alongOf = (b: Box): number => (lr ? b.x : b.y)
  const stepBoxes = boxes.filter((b) => b.kind === 'step')
  const rowsOrder = [...new Set(stepBoxes.map(alongOf))].sort((a, b) => a - b)
  const rowOf = new Map(stepBoxes.map((b) => [b.id, rowsOrder.indexOf(alongOf(b))]))
  /**
   * NOT SOLVED: a back edge left-right still crosses the ranks it climbs past.
   *
   * `routeTool -> awaitUser` is the one case in the corpus. Sending it around would need a corridor
   * OUTSIDE all content, and `smoothstep` computes its own midpoint, so there is nowhere to put one.
   * The fix is the phase this layout still lacks — Sugiyama's DUMMY VERTICES, which reserve space in
   * every layer an edge spans and hand the renderer a polyline to follow. That needs a waypoint-aware
   * edge component; until then the aesthetics gate allows exactly one such edge left-right and zero
   * top-down.
   */
  const routed = links.map((l) => {
    if (l.family !== 'control' || l.from === l.to) return l
    const a = rowOf.get(l.from)
    const b = rowOf.get(l.to)
    if (a === undefined || b === undefined) return l
    return b - a > 1 ? { ...l, route: 'side' as const } : l
  })

  const onScreen = new Set(boxes.map((b) => b.id))
  const notes = coverageNotes(flow)
  const rpcNote = rpcCoverageNote(flow)
  if (rpcNote !== undefined) notes.unshift(rpcNote)
  if (aside.size > 0) {
    notes.unshift(
      `${aside.size} recovery step${aside.size === 1 ? '' : 's'} in the right-hand gutter — reachable only by failing into them.`,
    )
  }

  return {
    boxes,
    links: [
      ...routed,
      // Row and lane, so only a gate with a nearer gate IN ITS OWN ROW routes around.
      ...gateLinks(flow, onScreen, (id) => {
        const p = posOfStep.get(id)
        if (p === undefined) return undefined
        return lr ? { row: p.x, lane: p.y } : { row: p.y, lane: p.x }
      }),
    ],
    bands,
    ...boundsOf(boxes, bands),
    notes,
  }
}

export const controlTopologyView: ViewSpec = {
  id: 'control',
  letter: 'A',
  label: 'Control topology',
  principle: 'StepDecisions form a directed graph over Steps. That is the literal control structure.',
  claim: 'The only view that answers "what happens next", and the base for run status.',
  risk: 'Loop-heavy and hub-prone, and silent about who has to act unless colour carries it.',
  layout,
}
