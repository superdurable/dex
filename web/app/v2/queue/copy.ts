// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Every string the queue can say, in one table so the wording can be audited.
 *
 * Nothing here claims a person is needed. A Flow Definition Graph declares when an Action
 * may be offered, not who must act, so the queue says what it read and where it read it.
 */
export const QUEUE_COPY = {
  appName: 'Inbox',
  strapline: 'What has arrived for you, read from the running process.',
  noGraph: 'No process diagram here by design: this view shows the work, not the shape of the process.',

  /** Derived from the live filter rows: the sentence must not outlive a filter the reader deleted. */
  scope(clauses: readonly string[]): string {
    if (clauses.length === 0) return 'Showing every run of this Flow type, open or closed.';
    return `Showing runs where ${clauses.join(' and ')}.`;
  },
  unfilteredHint: 'Closed runs are not work. Filter on execution status to hide them.',

  /** Counts describe the page, never the queue: the server paginates and we do not total it. */
  onThisPage(count: number): string {
    return count === 1 ? '1 run on this page' : `${count} runs on this page`;
  },
  clear: 'Nothing open on this page.',
  loading: 'Asking the process…',
  unreachable: 'Cannot reach the process, so this list is not the whole picture.',
  stale: 'Showing the last answer — the process did not respond just now.',
  staleShort: 'stale',
  refresh: 'Ask again',

  /** Actions are gated on live Attributes, so the list cannot promise one is available. */
  actionsProvenance: 'Which Actions are available is decided per run when you open it.',

  selectPrompt: 'Select a run to see what it reports and which Actions are available.',
  seeProcess: 'See the process',

  /**
   * Dex routes a Flow RPC to the worker that owns the run, so this run is unreadable for
   * good while the server itself is fine.
   */
  stranded:
    'This one cannot be read or acted on: it was started by a worker that is no longer running, so nothing can reach it. The process itself is fine.',
  strandedRow: 'cannot be reached',
} as const;
