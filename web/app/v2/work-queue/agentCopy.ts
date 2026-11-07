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
  reading: 'Reading the session…',
  selectPrompt: 'Select a session to see what it asks and answer it.',
  lastMessage: 'Last message',
  noMessage: 'No message with text yet.',
  pendingCalls(count: number): string {
    return count === 1 ? 'Waiting for you' : `Waiting for you (${count})`;
  },
  nothingPending: 'Nothing is waiting in this session now.',
  tool: 'Tool',
  arguments: 'Arguments',
  effect: 'Effect',
  deadline: 'Deadline',
  noDeadline: 'No deadline',
  approve: 'Approve',
  reject: 'Reject',
  rejectHeading: 'Reject this call',
  rejectReason: 'Tell the agent why (optional)',
  cancel: 'Cancel',
  answer: 'Answer',
  answerLabel: 'Your answer',
  decline: 'Decline',
  working: 'Working…',
  openSession: 'Open session',
  close: 'Close this session',
  needsPermission(permission: string): string {
    return `Choose Working as ${permission} to act.`;
  },
  undeclared(rpcName: string): string {
    return `This Flow's graph does not declare the ${rpcName} Action.`;
  },
  ended: 'The session has ended, so nothing can be answered.',
  alreadyHandled: 'Already handled',
  notApplied: 'Not applied',
  alreadyHandledWhy: 'Someone else answered this request, or its deadline passed. Showing the latest state.',
  deadlinePassed: 'Its deadline passed, so the session rejected the call. Showing the latest state.',
  stillWaitingAfterLock: 'Someone else was acting on this session at the same moment, so nothing changed. It is still waiting; try again.',
  notAccepted: 'The session did not accept this answer. It is still waiting.',
  done: 'Answered',
} as const;
