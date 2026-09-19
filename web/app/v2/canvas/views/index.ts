// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { controlTopologyView } from './controlTopology'
import type { ViewSpec } from './types'

/**
 * ONE view. The comparison is over.
 *
 * Six views and three compositions were built to open the information-hierarchy question, and A —
 * Control Topology — is the answer. The others are deleted rather than hidden, because a dead option
 * behind a flag still costs every future change a decision about whether to update it.
 *
 * What each one contributed, so the reasoning survives its code:
 *
 *   - B  Actor lanes    — that "who must act" is worth colour, not geometry. Lanes cost the whole
 *                         cross axis to say what an accent on the card already says, and transitions
 *                         crossed them constantly. Its one irreplaceable idea, the OUTSIDE THE FLOW
 *                         lane, survives as the external-RPC pennant in A.
 *   - C  Commit timeline— that a definition has no single commit order, so the view could only ever
 *                         be honest against a live run. That role now belongs to the panel's
 *                         Executions section, which is a list and does not need a canvas.
 *   - D  Resource field — that inverting figure and ground answers "why is this stuck" well and
 *                         "what happens next" not at all. Answered instead by the gate card's
 *                         `answered through` row and the panel's State section.
 *   - E  Nested         — that `parentId` is only two levels deep, so the containment ladder runs out
 *                         before it earns a view.
 *   - F  Gate board     — that "what needs me now" is a LIST, not a graph. It belongs in the chat
 *                         pane's rail, which is where it is going.
 *
 * The registry shape is kept even at length one: `viewById` still has to resolve a URL, and a single
 * hard-coded import would put that fallback in the caller.
 */
export const VIEWS: ViewSpec[] = [controlTopologyView]

export const ALL_VIEWS: ViewSpec[] = VIEWS

export function viewById(id: string): ViewSpec {
  return ALL_VIEWS.find((v) => v.id === id) ?? (VIEWS[0] as ViewSpec)
}
