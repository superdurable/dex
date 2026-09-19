// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { JSX } from 'react'
import type { Detail, Direction } from '../canvas/views/types'
import { DETAIL_LABEL, DETAILS, DIRECTION_LABEL, DIRECTIONS } from '../canvas/views/types'

/**
 * The control surface, after the research.
 *
 * It used to carry a four-position zoom-tier ladder, an exclusive "what do the arrows mean" mode,
 * six overlay checkboxes and two three-way reveal controls. All of that is gone, and none of it was
 * removed for tidiness:
 *
 *  - Detail is TWO positions and explicit, because nine surveyed products expose level-of-detail as
 *    a dedicated control while zoom stays geometric, and Copilot Studio's is literally
 *    `Expand/Collapse`.
 *  - The overlays went because no surveyed product draws state or transport as a node, and the
 *    measurements agreed: 27 of 29 channels have one consumer and 10 have no publisher at all.
 *  - The arrow mode went with them — with resources gone as nodes there is one answer left.
 *
 * What remains is how much detail and which direction. Definition-versus-run left with the `Showing`
 * axis: it is the MODE now, and a mode is not a view control.
 *
 * The FLOW PICKER went too, into its own clearly-marked harness control. It is not a product control at
 * all: a real session is about ONE flow, opened from a link or a chat turn, and a switcher sitting among
 * genuine controls implies the opposite. The view picker went for a different reason — with the comparison settled there is one view, and a radio group of one is a
 * label pretending to be a choice.
 */
export function Controls({
  detail,
  direction,
  onDetail,
  onDirection,
}: {
  detail: Detail
  direction: Direction
  onDetail: (d: Detail) => void
  onDirection: (d: Direction) => void
}): JSX.Element {
  const radio = (
    group: string,
    items: { id: string; label: string; title?: string }[],
    current: string,
    pick: (id: string) => void,
  ): JSX.Element => (
    <div className="pctl-row" role="radiogroup" aria-label={group}>
      {items.map((i) => (
        <button
          key={i.id}
          type="button"
          role="radio"
          aria-checked={current === i.id}
          className="pchip"
          data-on={current === i.id ? 'true' : undefined}
          title={i.title}
          onClick={() => pick(i.id)}
        >
          {i.label}
        </button>
      ))}
    </div>
  )

  return (
    <div className="pctl">
      <div className="pctl-group">
        <span className="pctl-label">Detail</span>
        {radio(
          'Detail',
          DETAILS.map((d) => ({
            id: d,
            label: DETAIL_LABEL[d],
            title:
              d === 'collapsed'
                ? 'Name, role and status only'
                : 'Adds what it waits for and where it goes. Zoom is separate and purely geometric.',
          })),
          detail,
          (id) => onDetail(id as Detail),
        )}
      </div>

      {/*
        THE `Showing` AXIS IS GONE, and its absence is the point.

        It was `How it works | What it did` — a view switch over one flow, which meant four combinations for
        two meanings and two of them were nonsense: editing while painting a run, and running while hiding
        it. Each mode has exactly ONE view now. Editing shows how the flow works; running shows what it is
        doing. The lifecycle control in the header is the only thing that changes which, so there is one
        place to look and no way to end up in a state that has to be explained.

        What survives here is what is genuinely about the DRAWING rather than about the flow: how much detail,
        and which way it runs.
      */}
      <div className="pctl-group">
        <span className="pctl-label">Direction</span>
        {radio(
          'Direction',
          DIRECTIONS.map((d) => ({
            id: d,
            label: DIRECTION_LABEL[d],
            title:
              'A setting rather than a decision — Airflow persists this per graph because long pipelines and wide fan-outs want different answers.',
          })),
          direction,
          (id) => onDirection(id as Direction),
        )}
      </div>

    </div>
  )
}
