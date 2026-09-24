// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { JSX } from 'react'
import type { PocFlow } from '../canvas/model/pocFlow'
import { ACTOR_LABEL, ACTOR_ORDER } from '../canvas/model/pocFlow'
import type { Scene } from '../canvas/views/types'

/**
 * A legend, DERIVED from what is actually on screen.
 *
 * It exists because of a documented failure elsewhere: Temporal split its history view in
 * two, lost the legend in the process, and had to add it back two releases later — then put
 * it in both views. This canvas encodes who-must-act as colour and edge family as a dash
 * pattern, neither of which is guessable, so the same trap was open here.
 *
 * Derived rather than static: a legend that lists things not on screen is noise, and worse,
 * it teaches a vocabulary the current picture does not use. Rows appear only when the scene
 * contains them, and counts come from the scene so the legend doubles as a census.
 */

const EDGE_LABEL: Record<string, string> = {
  control: 'what happens next',
  failure: 'failure path',
  gate: 'reached in from outside',
  rpc: 'reached in from outside',
  wait: 'waited on',
  resource: 'reads / writes',
  subflow: 'starts another flow',
}

/** Same order the eye should read them in: control flow first, data last. */
const EDGE_ORDER = ['control', 'failure', 'rpc', 'gate', 'wait', 'resource', 'subflow']

export function Legend({ scene, flow }: { scene: Scene; flow: PocFlow }): JSX.Element | null {
  const onScreen = new Set(scene.boxes.map((b) => b.id))

  // Actors, counted from the boxes actually drawn rather than from the flow — a view that
  // omits steps must not claim them.
  const actorCounts = new Map<string, number>()
  for (const b of scene.boxes) {
    if (b.actor === undefined) continue
    actorCounts.set(b.actor, (actorCounts.get(b.actor) ?? 0) + 1)
  }

  const drawn = scene.links.filter(
    (l) => l.latent !== true && onScreen.has(l.from) && onScreen.has(l.to),
  )
  const latent = scene.links.filter(
    (l) => l.latent === true && onScreen.has(l.from) && onScreen.has(l.to),
  )
  const latentControl = latent.filter((l) => l.family === 'control' || l.family === 'failure').length
  const latentData = latent.length - latentControl
  const edgeCounts = new Map<string, number>()
  for (const l of drawn) edgeCounts.set(l.family, (edgeCounts.get(l.family) ?? 0) + 1)

  const marks: { key: string; label: string }[] = []
  if (scene.boxes.some((b) => b.isStart === true)) {
    marks.push({ key: 'start', label: 'where it begins' })
  }
  if (scene.boxes.some((b) => b.emphasis === 'hub')) {
    marks.push({ key: 'hub', label: 'many steps return here' })
  }
  if (flow.steps.some((s) => s.isRecoveryHub)) {
    marks.push({ key: 'recovery', label: 'catches failures' })
  }
  // The external RPC is a node CLASS, not an actor, so it gets a mark rather than an actor swatch.
  if (scene.boxes.some((b) => b.kind === 'rpc')) {
    marks.push({ key: 'rpc', label: 'an external call, pointed at what it unblocks' })
  }

  /**
   * Run statuses, decoded from the TWO-CELL TOKEN — not from `status`, which now carries the ×N count.
   * Reading `status` here briefly made the legend list "×2" as if it were a status, which is exactly
   * the kind of noise a derived legend exists to avoid.
   *
   * Counted per PHASE, because that is what the cells encode: a Step can be waiting and not-started at
   * the same moment and both facts are on screen.
   */
  const statusCounts = new Map<string, number>()
  for (const b of scene.boxes) {
    if (b.token === undefined) {
      // Views that render a plain status word rather than the token (view C's commit rows).
      if (b.status !== undefined) statusCounts.set(b.status, (statusCounts.get(b.status) ?? 0) + 1)
      continue
    }
    if (b.token.wait !== null) {
      const k = `wait ${b.token.wait.status}`
      statusCounts.set(k, (statusCounts.get(k) ?? 0) + 1)
    }
    const k = `run ${b.token.execute.status}`
    statusCounts.set(k, (statusCounts.get(k) ?? 0) + 1)
  }

  if (
    actorCounts.size === 0 &&
    edgeCounts.size === 0 &&
    marks.length === 0 &&
    statusCounts.size === 0
  ) {
    return null
  }

  return (
    <section className="plegend">

      {[...actorCounts.keys()].length > 0 ? (
        <ul className="plegend-list">
          {ACTOR_ORDER.filter((a) => actorCounts.has(a)).map((a) => (
            <li key={a}>
              <span className="plegend-swatch" data-actor={a} />
              <span className="plegend-text">{ACTOR_LABEL[a]}</span>
              <span className="plegend-count">{actorCounts.get(a)}</span>
            </li>
          ))}
        </ul>
      ) : null}

      {edgeCounts.size > 0 ? (
        <ul className="plegend-list">
          {EDGE_ORDER.filter((f) => edgeCounts.has(f)).map((f) => (
            <li key={f}>
              <span className="plegend-line" data-family={f} />
              <span className="plegend-text">{EDGE_LABEL[f] ?? f}</span>
              <span className="plegend-count">{edgeCounts.get(f)}</span>
            </li>
          ))}
          {/*
            Latent edges are hidden for TWO different reasons, and saying so matters: a
            control edge is quiet because it converges on a hub, a resource edge because
            dataflow is 61% of the graph. One shared label claimed a hub existed on flows
            that have none.
          */}
          {latentControl > 0 ? (
            <li>
              <span className="plegend-line" data-family="latent" />
              <span className="plegend-text">hidden until you click the hub</span>
              <span className="plegend-count">{latentControl}</span>
            </li>
          ) : null}
          {latentData > 0 ? (
            <li>
              <span className="plegend-line" data-family="latent" />
              <span className="plegend-text">shown when you click a step</span>
              <span className="plegend-count">{latentData}</span>
            </li>
          ) : null}
        </ul>
      ) : null}

      {statusCounts.size > 0 ? (
        <ul className="plegend-list">
          {[...statusCounts].map(([status, count]) => (
            <li key={status}>
              <span className="plegend-status">{status}</span>
              <span className="plegend-count">{count}</span>
            </li>
          ))}
        </ul>
      ) : null}

      {marks.length > 0 ? (
        <ul className="plegend-list">
          {marks.map((m) => (
            <li key={m.key}>
              <span className="plegend-mark" data-mark={m.key} />
              <span className="plegend-text">{m.label}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  )
}
