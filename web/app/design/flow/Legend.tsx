// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { JSX } from 'react'
import type { PocFlow } from '../canvas/model/pocFlow'
import { TileView } from '../canvas/render/CardParts'
import { Glyph } from '../canvas/render/Glyph'
import type { BoxChip, LinkFamily, Scene, Tile } from '../canvas/views/types'

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

/** What a tile means, keyed as `tileKey` names it, in legend order. */
const TILE_MEANING: [string, string][] = [
  ['person', 'waits for a person'],
  ['door', 'waits for a message from outside'],
  ['wait', 'waits on something the analyser could not name'],
  ['clock', 'waits for time'],
  ['child', 'waits for child flows'],
  ['spark', 'asks an AI model'],
  ['app', 'calls an app through a connector'],
  ['cpu', 'runs automatically'],
]

function tileKey(tile: Tile): string {
  return 'glyph' in tile ? tile.glyph : 'app'
}

const SAMPLE_TILE: Record<string, Tile> = {
  person: { glyph: 'person', tone: 'attention' },
  door: { glyph: 'door', tone: 'door' },
  wait: { glyph: 'wait', tone: 'attention' },
  clock: { glyph: 'clock', tone: 'clock' },
  child: { glyph: 'child', tone: 'child' },
  spark: { glyph: 'spark', tone: 'accent' },
  app: { monogram: 'AP', tone: 'app' },
  cpu: { glyph: 'cpu', tone: 'machine' },
}

const LINE_LABEL: Record<LinkFamily, string> = {
  control: 'what happens next',
  failure: 'where a failure goes',
}

const CHIP: Record<BoxChip['kind'], { sample: string; label: string }> = {
  role: { sample: '◆', label: 'what the step is for in an agent' },
  answer: { sample: 'Approve', label: 'an Action that answers the step' },
  entry: { sample: '⚡ call', label: 'a call that starts the step' },
  child: { sample: '↳ flow', label: 'a child flow the step starts' },
  inbound: { sample: '← 6', label: 'steps that lead here' },
  fork: { sample: 'Runs 2 paths', label: 'starts every path at once' },
  jump: { sample: '↪ step', label: 'a jump to a step drawn elsewhere' },
  failJump: { sample: '↪ step', label: 'where a failure jumps' },
  ends: { sample: '✓', label: 'completes the flow' },
  stops: { sample: '■', label: 'stops one branch of the flow' },
  fails: { sample: '✕', label: 'fails the flow' },
}

function increment(counts: Map<string, number>, key: string): void {
  counts.set(key, (counts.get(key) ?? 0) + 1)
}

export function Legend({ scene, flow }: { scene: Scene; flow: PocFlow }): JSX.Element | null {
  const tiles = new Map<string, number>()
  const chips = new Map<string, number>()
  for (const box of scene.boxes) {
    if (box.tile) increment(tiles, tileKey(box.tile))
    for (const tile of box.stage?.tiles ?? []) increment(tiles, tileKey(tile))
    for (const row of box.chips ?? []) for (const chip of row) increment(chips, chip.kind)
  }
  const lines = new Map<string, number>()
  for (const link of scene.links) increment(lines, link.family)
  const stages = scene.boxes.filter((box) => box.kind === 'stage').length

  const marks: { key: string; label: string }[] = []
  if (scene.boxes.some((b) => b.isStart === true)) marks.push({ key: 'start', label: 'where the flow begins' })
  if (scene.boxes.some((b) => b.loops === true || b.stage?.loops === true)) {
    marks.push({ key: 'loop', label: 'runs again on its own' })
  }
  if (flow.steps.some((s) => s.isRecoveryHub)) marks.push({ key: 'recovery', label: 'catches failures' })
  if (scene.boxes.some((b) => b.kind === 'rpcCard' && b.rpcRole === undefined)) {
    marks.push({ key: 'rpc-card', label: 'an RPC and the state it reads and writes' })
  }
  if (scene.boxes.some((b) => b.rpcRole === 'action')) {
    marks.push({ key: 'action', label: 'an Action RPC operators can run' })
  }
  if (scene.boxes.some((b) => b.rpcRole === 'view')) {
    marks.push({ key: 'view', label: 'a Summary or Display view RPC' })
  }

  if (tiles.size === 0 && lines.size === 0 && chips.size === 0 && marks.length === 0 && stages === 0) return null

  return (
    <section className="plegend">
      {tiles.size > 0 ? (
        <>
          <h4 className="plegend-head">Who acts</h4>
          <ul className="plegend-list">
            {TILE_MEANING.filter(([key]) => tiles.has(key)).map(([key, label]) => (
              <LegendRow
                key={key}
                data={{ 'data-tile': key }}
                swatch={
                  <span className="plegend-tile">
                    <TileView tile={SAMPLE_TILE[key] as Tile} />
                  </span>
                }
                label={label}
                count={tiles.get(key)}
              />
            ))}
          </ul>
        </>
      ) : null}

      {stages > 0 || lines.size > 0 || marks.length > 0 ? (
        <>
          <h4 className="plegend-head">Shapes</h4>
          <ul className="plegend-list">
            {stages > 0 ? (
              <LegendRow
                swatch={
                  <span className="plegend-stage" aria-hidden="true">
                    ⊕
                  </span>
                }
                label="a stage: ⊕ shows its steps"
                count={stages}
              />
            ) : null}
            {(['control', 'failure'] as const)
              .filter((family) => lines.has(family))
              .map((family) => (
                <LegendRow
                  key={family}
                  swatch={<span className="plegend-line" data-family={family} />}
                  label={LINE_LABEL[family]}
                  count={lines.get(family)}
                />
              ))}
            {marks.map((m) => (
              <LegendRow
                key={m.key}
                swatch={
                  m.key === 'start' || m.key === 'loop' ? (
                    <Glyph name={m.key === 'start' ? 'bolt' : 'refresh'} className="plegend-glyph" />
                  ) : (
                    <span className="plegend-mark" data-mark={m.key}></span>
                  )
                }
                label={m.label}
              />
            ))}
          </ul>
        </>
      ) : null}

      {chips.size > 0 ? (
        <>
          <h4 className="plegend-head">On a card</h4>
          <ul className="plegend-list" data-swatch="chip">
            {(Object.keys(CHIP) as BoxChip['kind'][])
              .filter((kind) => chips.has(kind))
              .map((kind) => (
                <LegendRow
                  key={kind}
                  data={{ 'data-chip': kind }}
                  swatch={
                    <span className="pdock" data-chip={kind}>
                      {CHIP[kind].sample}
                    </span>
                  }
                  label={CHIP[kind].label}
                  count={chips.get(kind)}
                />
              ))}
          </ul>
        </>
      ) : null}
    </section>
  )
}

/** One legend row: a swatch cell of fixed width, a wrapping label, and an optional census count. */
function LegendRow({
  swatch,
  label,
  count,
  data,
}: {
  swatch: JSX.Element
  label: string
  count?: number
  data?: Record<string, string>
}): JSX.Element {
  return (
    <li {...data}>
      <span className="plegend-swatch">{swatch}</span>
      <span className="plegend-text">{label}</span>
      {count === undefined ? null : <span className="plegend-count">{count}</span>}
    </li>
  )
}
