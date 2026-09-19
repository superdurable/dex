// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { PocFlow } from '../model/pocFlow'
import type { Box, Link, ViewOpts } from './types'

/**
 * Pieces every view needs.
 *
 * What USED to be here and is now deliberately gone: a resource rail, a channel rail, a stream
 * rail, and an RPC rail. None of the surveyed products draws state or transport as a node, and the
 * measurements say why for our data — 27 of 29 channels have exactly one consumer and 10 have no
 * publisher at all, so a channel node is usually an orphan box duplicating a row that already
 * exists on the Step that waits for it. Those facts moved onto the gate card instead
 * (`stepBox.answeredBy`).
 *
 * What survives as a node: Steps, SubFlows (a boundary of the world), the ~2-in-26 RPCs that start a
 * Step directly, and — reversing part of the above — every RPC that WRITES TO A CHANNEL.
 *
 * That reversal is deliberate and narrow. The case for removing rails was that a channel or attribute
 * node is an orphan box duplicating a row that already exists on the Step. A channel-writing RPC is
 * not that: it is the door the outside world comes through, it has no inbound control edge by
 * construction, and it is the only thing in a Dex Flow that can make a blocked Step proceed. Reducing
 * it to an "answered through X" row states the fact but loses the topology — you cannot see that ONE
 * call opens TWO gates, which is exactly what job-post's `update` does.
 */

export const ORIGIN_W = 176

/**
 * Control links. Recovery is ALWAYS included — it is not a toggle.
 *
 * Nobody hides failure paths: Zapier draws error handling as a permanent Path with fixed
 * Success/Error labels, n8n as an opt-in output port that changes the node's geometry, and Dex's own
 * modeling guidance says failure behaviour belongs in the graph. The real complaint about recovery
 * edges was never visibility, it was ROUTING — they reach backwards across the whole happy path.
 * So they are marked `aside`, and the renderer sends them down a side gutter instead of through the
 * column.
 *
 * Hub-inbound edges stay LATENT: present in the scene, quiet until the hub or a neighbour is
 * selected. One Step in the real flow absorbs 16 of 27 transitions, and drawing all of them is a
 * star nobody can trace, while removing them would misrepresent the flow as a chain.
 */
export function controlLinks(flow: PocFlow, opts: ViewOpts): Link[] {
  const hubs = new Set(flow.steps.filter((s) => s.isHub).map((s) => s.id))
  /**
   * Only a DISPATCHER's outbound needs quietening. A sink has no outbound to quieten, and a fallback
   * has one feeder and usually one exit — hiding either would be hiding the whole point of it.
   */
  const blanket = new Set(
    flow.steps.filter((s) => s.hasUniformFailurePolicy).map((s) => s.id),
  )
  const dispatchers = new Set(
    flow.steps.filter((s) => s.recoveryRole === 'dispatcher').map((s) => s.id),
  )
  /**
   * The traversal count per edge, so a path that used one arrow twice reads as twice.
   *
   * Keyed on the pair and holding the FIRST ordinal, because the first time the run took an arrow is what
   * orders the drawing; the count is what says it looped.
   */
  const walked = new Map<string, number>()
  for (const hop of opts.path ?? []) {
    const key = `${hop.from}->${hop.to}`
    if (!walked.has(key)) walked.set(key, hop.ordinal)
  }

  return flow.transitions.map((t) => ({
    id: t.id,
    from: t.fromStepId,
    to: t.toStepId,
    family: t.kind === 'failure_transition' ? ('failure' as const) : ('control' as const),
    /**
     * Edge labels carry a COUNT, never a guard and never a case value.
     *
     * They used to print every merged guard joined together, which on the real Go flow meant several
     * 180-character expressions on one line — the same "no values on the canvas" violation the card
     * was already fixed for, just moved to the edges where the guard-test does not look. The full
     * condition text belongs in the panel.
     *
     * A CASE VALUE WAS TRIED HERE AND REMOVED, and the reason is a fact about our data rather than a
     * matter of taste. BPMN tells you to "label the path with the answer", so once a card names the one
     * name every branch tests, `target == INIT` on the edge ought to reduce to `INIT`. Built it,
     * looked at it: in both real switches the case value simply NAMES ITS DESTINATION —
     * `continueAwaitToolApproval` points at `AwaitToolApproval`, `INIT` at `InitStep` — so the label
     * restated the card it pointed at, while the edge itself ran through the text. The hoisted name on
     * the source card plus the named target card already carry it; see `stepBox`'s choice row.
     *
     * So: nothing when a single unconditional transition, and a bare count when several branches
     * converge on the same target, because that convergence is a fact card geometry cannot express.
     */
    label:
      t.mergedGuards.length > 1 ? `${t.mergedGuards.length} conditions` : undefined,
    /** Kept for the panel to render; never drawn on the canvas. */
    detail: t.mergedGuards.length > 0 ? t.mergedGuards.join('  ·  ') : undefined,
    selfLoop: t.isSelfLoop,
    ...(walked.has(`${t.fromStepId}->${t.toStepId}`)
      ? { onPath: walked.get(`${t.fromStepId}->${t.toStepId}`) as number }
      : {}),
    /**
     * An edge LEAVING the hub is how the flow proceeds, so it stays drawn. Only the convergence is
     * quietened — and it reveals when THIS edge is relevant to the selection, not merely when
     * something somewhere is selected.
     *
     * The earlier version tested `selectedId === null`, so selecting any unrelated card un-hid all
     * sixteen inbound edges at once, which is the exact hairball latency exists to prevent.
     */
    /**
     * Latent both ways, for the two kinds of star.
     *
     * INBOUND to a control hub: one Step in the real agent flow absorbs 16 of 27 transitions, and
     * drawing them all is a star nobody can trace.
     *
     * OUTBOUND from a RECOVERY hub, which the book pipeline added: its gate can resume the volume at
     * ANY stage, so it emits 14 transitions to twelve different Steps. Those are worth having on demand
     * and ruinous permanently — they were 53 of the drawing's crossings on their own. "This can restart
     * the flow anywhere" is a fact you ask for, not one you read past.
     *
     * FAILURE EDGES INTO A BLANKET POLICY, which is the third case and the subtlest.
     *
     * "Nobody hides a failure path" is right, and this does not. When every eligible Step routes its
     * exhausted retries to the same target, the edges stop being a topology and become a POLICY: 11 of
     * them on the book pipeline, one per Step, none saying anything the others do not. The card says it
     * in words instead — louder than eleven thin lines — and selecting the target brings them all back.
     * A small saga's four edges stay drawn, because four are countable at a glance.
     *
     * ONLY the `failure_transition` edges. This flow ALSO has 7 guarded `go_to(RecoveryGate)` branches,
     * and quietening those too takes the drawing to 2 crossings — but they are authored control flow,
     * not policy. Each is a decision somebody wrote ("if no approved beat plans, park"), and each says
     * something different. Tried it, measured it, rejected it: hiding an author's branch to win four
     * crossings is buying tidiness with meaning.
     */
    /**
     * AN EDGE THE RUN TOOK IS NEVER LATENT. Latency exists to stop a hub drawing as an untraceable star,
     * and it is right for the seven edges the run did not take — but hiding the one it did would hide the
     * execution path to protect the drawing from itself. So the path wins, and everything else stays quiet.
     */
    latent:
      !walked.has(`${t.fromStepId}->${t.toStepId}`) &&
      ((hubs.has(t.toStepId) &&
        !hubs.has(t.fromStepId) &&
        opts.selectedId !== t.toStepId &&
        opts.selectedId !== t.fromStepId) ||
      (dispatchers.has(t.fromStepId) &&
        t.kind !== 'failure_transition' &&
        !dispatchers.has(t.toStepId) &&
        opts.selectedId !== t.fromStepId &&
        opts.selectedId !== t.toStepId) ||
      (t.kind === 'failure_transition' &&
        blanket.has(t.toStepId) &&
        opts.selectedId !== t.toStepId &&
        opts.selectedId !== t.fromStepId)),
  }))
}

/**
 * The human-input path, synthesized: an RPC that publishes to a channel a Step waits on.
 *
 * Encoded nowhere as a single edge — all 26 RPCs in the corpus are graph-isolated. Reconstructed by
 * joining `rpc -publish-> channel` with `channel -condition-> wait`, which is why the resource layer
 * has to be present for any of this to appear at all.
 */
export function gateLinks(
  flow: PocFlow,
  onScreen: Set<string>,
  /**
   * Where each step ended up, in ROW (along the ranks) and LANE (across them). Supplied by the view,
   * which is the only thing that knows.
   *
   * `around` is needed only when a nearer target is IN THE WAY, and that means sharing a row. Two gates
   * side by side in one row — job-post's boards — do block each other: the direct path to the far one
   * passes behind the near one, and the piece of line that emerges between them reads as an edge
   * between THEM. Gates in DIFFERENT rows do not block each other at all; the direct path runs down the
   * gutter the pennant already lives in.
   *
   * Routing everything but the nearest around was the first rule, and the real book pipeline showed why
   * it is too broad: one `approve` opens three gates nine rows apart, and sending two of them along a
   * corridor past the end of the flow crossed the entire chain.
   */
  positionOf?: (stepId: string) => { row: number; lane: number } | undefined,
): Link[] {
  const out: Link[] = []
  for (const entry of flow.entries) {
    if (!onScreen.has(entry.id)) continue
    const targets = [...new Set(entry.opensGates.flatMap((g) => g.stepIds))].filter((id) =>
      onScreen.has(id),
    )
    /** Targets that share a row with another target closer to the gutter, so something IS in the way. */
    const blocked = new Set<string>()
    if (positionOf !== undefined) {
      for (const id of targets) {
        const me = positionOf(id)
        if (me === undefined) continue
        const nearerInSameRow = targets.some((other) => {
          if (other === id) return false
          const o = positionOf(other)
          return o !== undefined && o.row === me.row && o.lane < me.lane
        })
        if (nearerInSameRow) blocked.add(id)
      }
    }
    for (const gate of entry.opensGates) {
      for (const stepId of gate.stepIds) {
        const around = blocked.has(stepId)
        out.push({
          id: `gate:${entry.id}->${stepId}`,
          from: entry.id,
          to: stepId,
          family: 'rpc',
          label: channelName(gate.channelId),
          ...(around ? { route: 'around' as const } : {}),
        })
      }
    }
  }
  return out
}

/**
 * `resource:channel:approvals` -> `approvals`. The id's namespace, not the name.
 *
 * Real `dexcli` output prefixes a resource id with `resource:`; the hand-derived fixtures written before
 * there was any real Python output did not, so a strip of `^channel:` alone worked on them and silently
 * missed on the first genuine one — the channel showed up on screen as `resource:channel:approvals`.
 * Both shapes are accepted rather than picking a side, because both are in the corpus.
 */
function channelName(channelId: string): string {
  return channelId.replace(/^(?:resource:)?channel:/, '')
}

export const RPC_W = 158
export const RPC_H = 46

export interface ExternalRpc {
  id: string
  name: string
  /** Every Step this call can unblock. More than one is the case a text row cannot express. */
  stepIds: string[]
  channels: string[]
}

/**
 * RPCs that write to a channel some Step waits on — the external-event class.
 *
 * This is the frontend half of a contract: the backend will annotate channel-writing RPCs directly,
 * at which point this derivation reads a flag instead of performing the join. Until then it is a pure
 * function of the wire, per the derivation register in `decode.ts`.
 *
 * READ-ONLY RPCs ARE DELIBERATELY EXCLUDED. `describe`, `get`, `get_with_strong_consistency` and the
 * five in the real Go flow return a value to their caller and cannot change what happens next. They
 * are an API surface, not a flow event, and BPMN makes the same cut: a message that does not affect
 * the process is not drawn in the process diagram. They remain in view D and in the panel.
 */
export function externalRpcs(flow: PocFlow): ExternalRpc[] {
  const out: ExternalRpc[] = []
  for (const entry of flow.entries) {
    if (entry.kind !== 'rpc') continue
    const stepIds = [...new Set(entry.opensGates.flatMap((g) => g.stepIds))]
    if (stepIds.length === 0) continue
    out.push({
      id: entry.id,
      name: entry.name,
      stepIds,
      channels: [...new Set(entry.opensGates.map((g) => channelName(g.channelId)))],
    })
  }
  return out.sort((a, b) => a.name.localeCompare(b.name))
}

/**
 * Places each external RPC beside the FIRST step it opens, on the gutter axis.
 *
 * Aligned to a step rather than stacked in a rail, because the alignment is what makes the arrow short
 * and the association pre-attentive. When one call opens several gates the extra edges fan out from
 * the same node, which is the fact worth seeing.
 */
export function externalRpcBoxes(
  flow: PocFlow,
  gutter: number,
  /** The CENTRE of the step on the gutter axis, not its top edge — see below. */
  centreOf: (stepId: string) => number | undefined,
  lr: boolean,
): Box[] {
  const boxes: Box[] = []
  const used: number[] = []
  for (const rpc of externalRpcs(flow)) {
    /**
     * Centred on the step it feeds, not top-aligned to it.
     *
     * A pennant is 46px and a step card is 52px or more, so top-aligning them left their centre
     * handles a few pixels apart — and the connector picked up a visible kink for no reason at all.
     * Aligning centres makes the common case a straight line.
     */
    const centre = rpc.stepIds.map(centreOf).find((v) => v !== undefined)
    if (centre === undefined) continue
    const anchor = centre - (lr ? RPC_W : RPC_H) / 2
    // Nudge off a taken slot rather than overlapping. Two RPCs opening the same step is legal.
    let at = anchor
    while (used.some((u) => Math.abs(u - at) < (lr ? RPC_W : RPC_H) + 10)) at += (lr ? RPC_W : RPC_H) + 10
    used.push(at)
    boxes.push({
      id: rpc.id,
      x: lr ? at : gutter,
      y: lr ? gutter : at,
      w: RPC_W,
      // Pointing down costs 13px of the bottom edge, so the box grows rather than clipping its own
      // subtitle — which it did, and only a screenshot showed it.
      h: lr ? RPC_H + 14 : RPC_H,
      kind: 'rpc',
      title: rpc.name,
      // The TYPE of thing it is, never a value — and it names the count when one call opens several.
      subtitle: rpc.stepIds.length > 1 ? `opens ${rpc.stepIds.length} gates` : 'external call',
      point: lr ? 'down' : 'right',
    })
  }
  return boxes
}

/**
 * The honest note for a flow whose RPCs cannot be joined to anything.
 *
 * Measured: the real Go flow declares five RPCs — `ApproveTool`, `SendMessage`, `SteerMessage`,
 * `ExecutePlan`, `Snapshot` — and three Steps whose waits the analyser could not resolve. Those are
 * self-evidently the same relationship, and yet ZERO edges join them, because `dexcli`'s Go analyser
 * emits control flow only and never emits the channels the join needs.
 *
 * So this returns text, not silence. A blank gutter on the partner's own flow would read as "this
 * flow has no external entry points", which is the opposite of true.
 */
export function rpcCoverageNote(flow: PocFlow): string | undefined {
  const rpcs = flow.entries.filter((e) => e.kind === 'rpc')
  if (rpcs.length === 0) return undefined
  if (externalRpcs(flow).length > 0) return undefined
  return `${rpcs.length} RPC${rpcs.length === 1 ? '' : 's'} exist but none reports writing to a channel, so we cannot say which steps they unblock — ${flow.resources.length === 0 ? 'this flow has no resource layer at all' : 'no channel write was reported'}.`
}

/** RPCs that start a Step directly. Measured: 2 of 26. Real origins, so they are drawn. */
export function originEntries(flow: PocFlow): { id: string; name: string; startsStepId: string }[] {
  const out: { id: string; name: string; startsStepId: string }[] = []
  for (const entry of flow.entries) {
    for (const b of entry.branches) {
      for (const t of b.targets) {
        out.push({ id: entry.id, name: entry.name, startsStepId: t.stepId })
      }
    }
  }
  return out
}

export function originBoxes(
  flow: PocFlow,
  x: number,
  yOf: (stepId: string) => number | undefined,
): Box[] {
  const seen = new Set<string>()
  const out: Box[] = []
  for (const o of originEntries(flow)) {
    if (seen.has(o.id)) continue
    const y = yOf(o.startsStepId)
    if (y === undefined) continue
    seen.add(o.id)
    out.push({
      id: o.id,
      x,
      y,
      w: ORIGIN_W,
      h: 42,
      kind: 'rpc',
      title: o.name,
      subtitle: 'starts this step',
    })
  }
  return out
}

/** SubFlows sit to the right, aligned to the Step that starts them. Never on a rank. */
export function subflowBoxes(
  flow: PocFlow,
  x: number,
  yOf: (stepId: string) => number | undefined,
): Box[] {
  return flow.subflows.map((s, i) => ({
    id: s.id,
    x,
    y: (s.startedByStepId === undefined ? undefined : yOf(s.startedByStepId)) ?? 60 + i * 60,
    w: ORIGIN_W,
    h: 44,
    kind: 'subflow' as const,
    title: s.flowType,
    subtitle: s.external ? 'external flow' : 'child flow',
  }))
}

/** Honest notes about what a given flow cannot show, so a blank area is never a mystery. */
export function coverageNotes(flow: PocFlow): string[] {
  const notes: string[] = []
  const gates = flow.steps.filter((s) => s.actor === 'external').length
  const unresolved = flow.steps.filter((s) => s.actor === 'unknown').length
  if (gates === 0 && unresolved > 0) {
    notes.push(
      `${unresolved} step${unresolved === 1 ? '' : 's'} wait on something the analyser could not name — any of them may need a person.`,
    )
  }
  /**
   * Disclose the collapse. Hiding edges without saying so is the one thing this canvas must not do, and
   * a blanket failure policy is the only case where it hides any.
   */
  for (const st of flow.steps) {
    if (!st.hasUniformFailurePolicy) continue
    notes.push(
      `Every step that can fail routes to ${st.label} — that is a policy, not ${st.inboundFailureCount} separate paths, so those edges stay hidden until you select it.`,
    )
  }
  if (flow.subflows.length === 0) notes.push('No child flows.')
  if (flow.dropped.length > 0) notes.push(`${flow.dropped.length} wire elements not modelled.`)
  return notes
}
