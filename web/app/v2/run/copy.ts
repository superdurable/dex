// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export const RUN_COPY = {
  runsHeading: 'Runs',
  noRuns: 'No runs of this Flow type.',
  /** The order is stated rather than left for the reader to infer. */
  order: 'Open first, then newest.',
  selectPrompt: 'Select a run to see where it is and what it needs.',
  /** The step the canvas is showing, which is not always the one that needs somebody. */
  showingStep: 'Showing',
  waitingAt: 'Waiting at',
  actionsHeading: 'Actions',
  displayHeading: 'Reported',
  noActions: 'No Actions are available in the current state.',
  stop: 'Stop',
  stopped: 'Stopping…',
  inspect: 'Inspect',
  inspectHint: 'The full technical record for this run',
  close: 'Close the run panel',
  elapsed: 'Running for',
  closed: 'Closed',
} as const;
