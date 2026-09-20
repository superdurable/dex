// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { PocFlow } from '../model/pocFlow'
import type { Band, Box, Direction, Scene } from './types'

/**
 * What a good drawing has to achieve, stated once and measured, so spacing stops being tuned
 * per screenshot. Each principle carries the proxy that can fail a test.
 */
export interface LayoutPrinciple {
  id: PrincipleId
  statement: string
  measure: string
  /** A scene violates the principle when its metric exceeds this. */
  limit: number
}

export type PrincipleId =
  | 'no-collision'
  | 'tight-regions'
  | 'group-cohesion'
  | 'short-edges'
  | 'reading-order'

/**
 * Lane coherence was measured and dropped, not forgotten.
 *
 * Grouping by `recoveryRole` claimed the refund flow split a class across lanes. It does not:
 * BillingFailedStep is reachable by three ordinary transitions, so the spine is correct for it.
 * The role is just too coarse to predict a lane, which made the metric accuse a truthful drawing.
 */

export const LAYOUT_PRINCIPLES: readonly LayoutPrinciple[] = [
  {
    id: 'no-collision',
    statement: 'Two cards never occupy the same space.',
    measure: 'Overlapping step-box pairs.',
    limit: 0,
  },
  {
    id: 'tight-regions',
    statement: 'A labelled region is mostly its own members, not empty floor.',
    measure: 'Worst region: fraction of its area holding no card.',
    // Padding and the label strip cost 0.49 on a single-card region; a staggered fan reaches 0.73.
    limit: 0.8,
  },
  {
    id: 'group-cohesion',
    statement: 'A group sits in one place where the graph allows it.',
    measure: 'Same-label regions at one rank split across two lanes.',
    limit: 0,
  },
  {
    id: 'short-edges',
    statement: 'An arrow spans about a rank, not the whole drawing.',
    measure: 'Mean edge length in rank-pitch units.',
    // Corpus worst is 5.7. Cohesion costs edge length, which is the tension this limit holds.
    limit: 6.5,
  },
  {
    id: 'reading-order',
    statement: 'The first card is the start of the flow.',
    measure: 'Cards placed before the start Step.',
    limit: 0,
  },
] as const

export interface LayoutMetrics {
  collisions: number
  /** Worst region's empty fraction, or 0 with no labelled region. */
  regionWaste: number
  straddledGroups: number
  edgeSpan: number
  beforeStart: number
}

export type Violation = { id: PrincipleId; value: number; limit: number }

const METRIC_OF: Record<PrincipleId, keyof LayoutMetrics> = {
  'no-collision': 'collisions',
  'tight-regions': 'regionWaste',
  'group-cohesion': 'straddledGroups',
  'short-edges': 'edgeSpan',
  'reading-order': 'beforeStart',
}

/** Which principles this scene breaks. Empty is the gate every corpus flow has to pass. */
export function violations(flow: PocFlow, scene: Scene, direction: Direction): Violation[] {
  const metrics = measureScene(flow, scene, direction)
  return LAYOUT_PRINCIPLES.filter((principle) => metrics[METRIC_OF[principle.id]] > principle.limit)
    .map((principle) => ({
      id: principle.id,
      value: round(metrics[METRIC_OF[principle.id]]),
      limit: principle.limit,
    }))
}

/**
 * Direction is required, not inferred.
 *
 * Ranks run down the page in 'tb' and across it in 'lr', so a metric that assumed one axis
 * reported nonsense for half the corpus.
 */
export function measureScene(flow: PocFlow, scene: Scene, direction: Direction): LayoutMetrics {
  const cards = scene.boxes.filter((box) => box.kind === 'step')
  const axes = direction === 'lr'
    ? {
      along: (card: Box) => card.x,
      extent: (card: Box) => card.w,
      across: (card: Box) => card.y,
      spanAlong: (rect: Rect) => ({ min: rect.x, max: rect.x + rect.w }),
    }
    : {
      along: (card: Box) => card.y,
      extent: (card: Box) => card.h,
      across: (card: Box) => card.x,
      spanAlong: (rect: Rect) => ({ min: rect.y, max: rect.y + rect.h }),
    }
  return {
    collisions: countCollisions(cards),
    regionWaste: worstRegionWaste(scene.bands, cards),
    straddledGroups: countStraddledGroups(scene.bands, cards, axes.across, axes.spanAlong),
    edgeSpan: meanEdgeSpan(scene, cards, axes.along),
    beforeStart: countBeforeStart(flow, cards, axes.along, axes.extent),
  }
}

type Reader = (card: Box) => number

function countCollisions(cards: readonly Box[]): number {
  let hits = 0
  for (let a = 0; a < cards.length; a++) {
    for (let b = a + 1; b < cards.length; b++) {
      if (overlaps(cards[a] as Box, cards[b] as Box)) hits++
    }
  }
  return hits
}

function overlaps(one: Rect, two: Rect): boolean {
  return one.x < two.x + two.w && two.x < one.x + one.w
    && one.y < two.y + two.h && two.y < one.y + one.h
}

interface Rect { x: number; y: number; w: number; h: number }

/**
 * Regions are judged by how much of their area is floor.
 *
 * This is the metric that catches a band stretched between two lanes, which reads as a region
 * with a hole in it rather than as a place.
 */
function worstRegionWaste(bands: readonly Band[], cards: readonly Box[]): number {
  const regions = bands.filter((band) => band.style === 'group')
  let worst = 0
  for (const region of regions) {
    const area = region.w * region.h
    if (area <= 0) continue
    const ink = cards
      .filter((card) => centreInside(card, region))
      .reduce((sum, card) => sum + card.w * card.h, 0)
    worst = Math.max(worst, 1 - Math.min(ink, area) / area)
  }
  return worst
}

function centreInside(card: Box, region: Rect): boolean {
  const cx = card.x + card.w / 2
  const cy = card.y + card.h / 2
  return cx >= region.x && cx <= region.x + region.w
    && cy >= region.y && cy <= region.y + region.h
}

/**
 * One group drawn as two regions side by side, at the same point in the flow.
 *
 * That is the shape a reader cannot explain: the same label twice at one rank with a lane between.
 * Members genuinely ranks apart are fine, and stay counted as separate places.
 */
function countStraddledGroups(
  bands: readonly Band[],
  cards: readonly Box[],
  across: Reader,
  spanAlong: (rect: Rect) => { min: number; max: number },
): number {
  const far = Math.max(...cards.map(across), 0)
  const regions = bands
    .filter((band) => band.style === 'group' && band.label !== undefined)
    .map((band) => {
      const inside = cards.filter((card) => centreInside(card, band))
      return {
        label: band.label as string,
        lane: inside.some((card) => across(card) >= far) ? 1 : 0,
        span: spanAlong(band),
      }
    })
  let straddles = 0
  for (let a = 0; a < regions.length; a++) {
    for (let b = a + 1; b < regions.length; b++) {
      const one = regions[a] as (typeof regions)[number]
      const two = regions[b] as (typeof regions)[number]
      if (one.label !== two.label || one.lane === two.lane) continue
      if (one.span.min < two.span.max && two.span.min < one.span.max) straddles++
    }
  }
  return straddles
}

/**
 * Length per edge, divided by the pitch between neighbouring ranks.
 *
 * Normalised so the number means the same thing for a 6-step flow and a 30-step one.
 */
function meanEdgeSpan(scene: Scene, cards: readonly Box[], along: Reader): number {
  const centres = new Map(cards.map((card) => [card.id, {
    x: card.x + card.w / 2,
    y: card.y + card.h / 2,
  }]))
  const spans = scene.links
    .filter((link) => centres.has(link.from) && centres.has(link.to) && link.from !== link.to)
    .map((link) => {
      const from = centres.get(link.from) as { x: number; y: number }
      const to = centres.get(link.to) as { x: number; y: number }
      return Math.abs(from.x - to.x) + Math.abs(from.y - to.y)
    })
  if (spans.length === 0) return 0
  const pitch = rankPitch(cards, along)
  const mean = spans.reduce((sum, span) => sum + span, 0) / spans.length
  return pitch <= 0 ? 0 : mean / pitch
}

/**
 * The TYPICAL gap between neighbouring rows, taken as the median.
 *
 * Not the minimum: a wrapped rank sits deliberately close, and dividing by that made a normal
 * drawing look as though every edge ran the length of the page.
 */
function rankPitch(cards: readonly Box[], along: Reader): number {
  const rows = [...new Set(cards.map((card) => Math.round(along(card))))].sort((a, b) => a - b)
  if (rows.length < 2) return 0
  const gaps = rows.slice(1)
    .map((row, index) => row - (rows[index] as number))
    .filter((gap) => gap > 0)
    .sort((left, right) => left - right)
  if (gaps.length === 0) return 0
  return gaps[Math.floor(gaps.length / 2)] as number
}

function countBeforeStart(
  flow: PocFlow,
  cards: readonly Box[],
  along: Reader,
  extent: Reader,
): number {
  const startId = flow.startStepId ?? flow.steps.find((step) => step.isStart)?.id
  const start = cards.find((card) => card.id === startId)
  if (start === undefined) return 0
  return cards.filter((card) => along(card) + extent(card) <= along(start)).length
}

function round(value: number): number {
  return Math.round(value * 1000) / 1000
}
