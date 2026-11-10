// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Band, Box } from '../views/types'

/**
 * Where the legend sits, as a pure function of the drawing.
 *
 * In its own module rather than inside `Stage` so it can be tested without loading React Flow, the same
 * reason `markers.ts` is separate.
 *
 * The rule, and what each part of it is for:
 *
 *   - BOTTOM-ALIGNED with the lowest CARD, not with the scene's bounding box. A band's padding extends a
 *     few pixels past its last member, and aligning to that sat visibly low. Anchoring the bottom rather
 *     than the top is what makes the legend look placed instead of attached: a top-pinned legend drifts
 *     away from the flow as the flow gets shorter.
 *   - RIGHT of whatever is level with the bottom, not right of everything. Using the overall right edge
 *     put the legend out past the SubFlow gutter — which sits high up — so it floated alone in empty
 *     space and read as an attachment. Measuring only the bottom band tucks it in beside the last Step.
 *   - A FIXED reserved height, so `fitView` has stable bounds. The card bottom-aligns inside that box in
 *     CSS, which is what lets it collapse to a chip and expand to a key without moving its bottom edge.
 */

/** Reserved box. Not the card's height — the card sits at the bottom of it. */
export const LEGEND_W = 292
export const LEGEND_BOX_H = 320

/** How far above the lowest card still counts as "level with the bottom". */
const BOTTOM_BAND = 140

/** Gap between the end of the flow and the legend. */
const GAP = 40

export function legendAnchor(
  boxes: Box[],
  bands: Band[],
): { x: number; y: number } | undefined {
  if (boxes.length === 0) return undefined
  const maxY = Math.max(...boxes.map((b) => b.y + b.h))
  const all = [...boxes, ...bands]
  const nearBottom = all.filter((b) => b.y + b.h > maxY - BOTTOM_BAND)
  const source = nearBottom.length > 0 ? nearBottom : boxes
  const y = maxY - LEGEND_BOX_H
  let x = Math.max(...source.map((b) => b.x + b.w)) + GAP

  /**
   * Then PUSH RIGHT until the reserved box is clear.
   *
   * The box is 320px tall and grows upward from the bottom, so on a short wide drawing it reaches back
   * into the content even though the bottom band was clear. Left-right on the book pipeline it landed on
   * HarvestStep — caught by the test, not by looking, because I had only checked top-down.
   *
   * Bottom alignment is the property worth keeping, so `y` never moves; `x` gives way instead. Bounded
   * by the item count because each pass clears at least one obstruction.
   */
  const hits = (): (Box | Band)[] =>
    all.filter((b) => x < b.x + b.w && b.x < x + LEGEND_W && y < b.y + b.h && b.y < y + LEGEND_BOX_H)
  for (let pass = 0; pass < all.length; pass++) {
    const blocking = hits()
    if (blocking.length === 0) break
    x = Math.max(...blocking.map((b) => b.x + b.w)) + GAP
  }
  return { x, y }
}
