// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * What the canvas publishes to whoever owns the keyboard.
 *
 * Lives with the canvas rather than in a layout module: it is a statement about what a viewport can do,
 * and the only implementer is `Stage`.
 */
export interface CanvasViewportHandle {
  zoomIn(): void
  zoomOut(): void
  /** Fit the whole graph, capped so a small graph does not open fully zoomed in. */
  fit(): void
  /** Current zoom, for the readout. */
  zoom(): number
  /** Pan the minimum distance that brings a node fully into view. Never zooms. */
  reveal(nodeId: string): void
  /** Zoom in on one node, for when reading it is the point. */
  focus(nodeId: string): void
}
