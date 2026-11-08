// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Every string the agent inbox can say, kept apart from Work Queue's so each can be audited. */
export const AGENT_INBOX_COPY = {
  runsMode: 'Runs',
  agentsMode: 'Agents',
  modeLabel: 'Work Queue mode',
  heading: 'Needs a person',
  waiting(count: number): string {
    return count === 1 ? '1 waiting' : `${count} waiting`;
  },
  empty: 'No session is waiting for a person.',
  /** A session is listed only when it opted into active Step search. */
  emptyWhy: 'Sessions are found by the Step they wait in. A session started without active Step search does not appear.',
  pageOnly(count: number): string {
    return `Showing the first ${count} sessions. The deadline order covers these only.`;
  },
  approval: 'Approval',
  question: 'Question',
  more(count: number): string {
    return `+${count} more`;
  },
  selectPrompt: 'Select a session to see what it asks and answer it.',
  lastMessage: 'Last message',
  noMessage: 'No message with text yet.',
  pendingCalls(count: number): string {
    return count <= 1 ? 'Waiting for you' : `Waiting for you (${count})`;
  },
  nothingPending: 'Nothing is waiting in this session now.',
  openSession: 'Open session',
  close: 'Close this session',
} as const;
