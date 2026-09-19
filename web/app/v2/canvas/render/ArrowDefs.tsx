// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { JSX } from 'react'

/**
 * The arrowheads, defined once for the whole document.
 *
 * Until now no edge on this canvas had a marker of any kind — every relationship was a bare line, so
 * the single most important thing a control graph carries, DIRECTION, was absent. That was a real
 * defect and it survived a full test suite, because geometry is not what tests look at.
 *
 * Two shapes, not six, and the distinction is BPMN 2.0's:
 *
 *   - SEQUENCE FLOW — flow proceeding inside the process — is a solid line with a SOLID FILLED
 *     arrowhead.
 *   - MESSAGE FLOW — something outside the process reaching in — is a dashed line with a HOLLOW
 *     arrowhead and a HOLLOW CIRCLE at the source end. BPMN forbids sequence flow from crossing a
 *     participant boundary at all, precisely so that "this came from outside" is never a matter of
 *     reading a label.
 *
 * That pairing is the most established convention available for exactly our problem, and it is
 * pre-attentive: filled-versus-hollow is a shape difference, so it survives greyscale, colour-blindness
 * and a 24px thumbnail in the compare grid.
 *
 * COLOUR IS NOT SET HERE. Every marker paints with `context-stroke`, the SVG 2 keyword for "whatever
 * stroke the path referencing me has". So an arrowhead inherits its edge's hue from the `--p-edge-*`
 * tokens automatically, and dims and lights with `.pedge-dim` / `.pedge-lit` for free. The first
 * version instead defined one marker per family with a literal hex, which duplicated the palette in a
 * file that cannot read it — exactly what the no-hex compliance check exists to catch. Four
 * definitions now cover seven families.
 *
 * SIZE IS ABSOLUTE. `markerUnits` defaults to `strokeWidth`, which multiplies the arrowhead by the
 * line's thickness — so the deliberately-thickest line on the canvas, a failure edge at 2.4px, got a
 * ~17px arrowhead, and `--zoom-comp` compounded it further. Every marker is `userSpaceOnUse`, so an
 * arrowhead is one size everywhere and thickness and head size stop fighting.
 *
 * Mounted once at the root rather than per Stage because SVG marker ids are document-scoped.
 */

export function ArrowDefs(): JSX.Element {
  return (
    <svg aria-hidden="true" focusable="false" width="0" height="0" style={{ position: 'absolute' }}>
      <defs>
        {/* SEQUENCE FLOW: the flow proceeds. One definition serves every control-ish family. */}
        <marker
          id="p-arrow-solid"
          viewBox="0 0 10 10"
          refX="9"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse"
        >
          <path d="M 0 1 L 9 5 L 0 9 z" fill="context-stroke" />
        </marker>

        {/*
          MESSAGE FLOW, target end: hollow. The interior is the CANVAS colour rather than `none`,
          because an unfilled arrowhead has the line it terminates running straight through it, which
          reads as a smear rather than as an outline.
        */}
        <marker
          id="p-arrow-hollow"
          viewBox="0 0 12 12"
          refX="10"
          refY="6"
          markerWidth="9"
          markerHeight="9"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse"
        >
          <path
            d="M 1 1.5 L 10 6 L 1 10.5 z"
            stroke="context-stroke"
            strokeWidth="1.4"
            style={{ fill: 'var(--p-bg)' }}
          />
        </marker>

        {/* MESSAGE FLOW, source end: the small hollow circle that says "this originates outside". */}
        <marker
          id="p-tail-hollow"
          viewBox="0 0 10 10"
          refX="5"
          refY="5"
          markerWidth="6"
          markerHeight="6"
          markerUnits="userSpaceOnUse"
          orient="auto"
        >
          <circle
            cx="5"
            cy="5"
            r="3.2"
            stroke="context-stroke"
            strokeWidth="1.4"
            style={{ fill: 'var(--p-bg)' }}
          />
        </marker>

        {/*
          ASSOCIATION — a resource touch. BPMN draws it as a bare open V with no triangle, which is
          the weakest arrowhead available, and that is the point: reading or writing state is not the
          flow proceeding, and it should not compete with an edge that is.
        */}
        <marker
          id="p-arrow-open"
          viewBox="0 0 10 10"
          refX="8"
          refY="5"
          markerWidth="7"
          markerHeight="7"
          markerUnits="userSpaceOnUse"
          orient="auto-start-reverse"
        >
          <path d="M 1 1.5 L 8 5 L 1 8.5" fill="none" stroke="context-stroke" strokeWidth="1.3" />
        </marker>
      </defs>
    </svg>
  )
}
