// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ToolCallState } from './sessionModel';

/** Every string the session view and its cards can say, in one table. */
export const SESSION_COPY = {
  tabCanvas: 'Canvas',
  tabSession: 'Session',
  tabsLabel: 'Run views',
  loading: 'Reading the session…',
  reload: 'Reload',
  user: 'User',
  assistant: 'Agent',
  noMessages: 'The session has no messages yet.',
  readFailed(reason: string): string {
    return `The session could not be read: ${reason}. The conversation is not shown, because it would look empty.`;
  },
  missingHeading: 'Some of this session could not be read',
  stale: 'Showing the last answer. The latest read failed:',
  pastCallsNote:
    'A session deletes its tool call records when a turn ends. Past calls inside the loaded Flow history show their record. Older calls show only what the message text keeps: no effect class, approval decision, or outcome unknown.',
  earlierMessages(count: number): string {
    return count === 1 ? 'Show earlier messages (1 stored chunk)' : `Show earlier messages (${count} stored chunks)`;
  },
  deletedBefore(sequence: number): string {
    return `Messages before ${sequence} were deleted by the session's retention.`;
  },
  chunkUnreadable(count: number): string {
    return count === 1 ? '1 stored chunk of earlier messages could not be read.' : `${count} stored chunks of earlier messages could not be read.`;
  },
  usageHeading: 'Usage',
  usage(input: number, output: number, cached: number, reasoning: number, calls: number): string {
    const reasoningPart = reasoning > 0 ? ` · ${reasoning.toLocaleString()} reasoning` : '';
    return `${calls} model ${calls === 1 ? 'call' : 'calls'} · ${input.toLocaleString()} input · ${output.toLocaleString()} output · ${cached.toLocaleString()} cached${reasoningPart} tokens`;
  },
  sessionStatus: {
    waiting_for_user_message: 'Waiting for a message',
    running_model: 'Model running',
    running_tools: 'Running tools',
    compacting_context: 'Compacting context',
    failed: 'Failed',
  } as Record<string, string>,
  waitingForApproval(count: number): string {
    return count === 1 ? 'Waiting for approval' : `${count} calls waiting for approval`;
  },
  waitingForAnswer(count: number): string {
    return count === 1 ? 'Waiting for an answer' : `${count} questions waiting for an answer`;
  },
  turn(turnId: string): string {
    return turnId.replace(/^turn-/, 'Turn ');
  },
  lastTurnError: 'Last turn error',
  writtenBy: 'Written by',
  steerLabel: 'Message to the agent',
  steerPlaceholder: 'Tell the agent what to do instead',
  steer: 'Steer',
  send: 'Send',
  steerIdleNote: 'No turn is running, so this message starts the next turn.',
  stop: 'Stop turn',
  stopping: 'Stopping…',
  sending: 'Sending…',
  noActiveTurn: 'No turn is running.',
  noTurnControl: 'This turn does not accept steering or stopping.',
  tellAgentPrefix(toolName: string, toolCallId: string): string {
    return `About ${toolName} (${toolCallId}): `;
  },
  steered: 'Message sent.',
  stopped: 'Stop requested. The turn ends after any tool call that has started.',
  refused(rpcName: string): string {
    return `The session did not take this request (${rpcName} returned false). Reload to see why.`;
  },
  noAction(rpcName: string): string {
    return `This Flow's definition declares no ${rpcName} Action.`;
  },
  closed: 'The session is closed.',
  chooseWorkingAs(permission: string): string {
    return `Choose Working as ${permission} to act.`;
  },
  workAs(permission: string): string {
    return `Work as ${permission}`;
  },
  busy: 'The session is busy. Try again.',
  noWorker: 'No Worker is connected to run this. Try again when Workers are back.',
  goReduced:
    'This session runs on Go Workers, which have no questions, steering, or stopping. The view shows messages, tool calls, approvals, and usage.',

  // Tool call cards.
  arguments: 'Arguments',
  output: 'Output',
  error: 'Error',
  effect: {
    read: 'read',
    idempotent_write: 'idempotent write',
    non_idempotent_write: 'non-idempotent write',
    unclassified: 'unclassified',
  } as Record<string, string>,
  toolCallId: 'Tool call ID',
  state: {
    awaiting_approval: 'waiting for approval',
    awaiting_answer: 'waiting for an answer',
    deciding: 'decision recorded',
    queued: 'queued for a Worker',
    write_queued: 'waiting for an earlier write',
    recording: 'recording the result',
    running: 'running',
    retrying: 'retrying',
    waiting: 'waiting',
    done: 'done',
    failed: 'failed',
    outcome_unknown: 'outcome unknown',
    skipped: 'skipped',
    rejected: 'rejected',
    cancelled: 'not run',
    returned: 'returned',
    no_result: 'no result kept',
  } satisfies Record<ToolCallState, string>,
  attempt(attempt: number | undefined, maxAttempts: number | undefined): string {
    if (attempt === undefined) return '';
    return maxAttempts === undefined ? `Attempt ${attempt}.` : `Attempt ${attempt} of ${maxAttempts}.`;
  },
  queuedDetail(reason: string | undefined, waited: string, attempt: number | undefined): string {
    const ranBefore = attempt !== undefined && attempt > 1;
    const notRun = ranBefore ? 'An earlier attempt failed.' : 'It has not run.';
    const wait = waited ? ` for ${waited}` : '';
    if (reason === 'locks') return `Waiting for locks${wait}. ${notRun}`;
    if (reason === 'supporting_worker') return `Waiting for a Worker that supports this Step type${wait}. ${notRun}`;
    return `Waiting for a Worker${wait}. ${notRun}`;
  },
  startingDetail: 'No Step is active for it right now.',
  recordingDetail(stepType: string): string {
    return `Its attempts ended without a result, so it may have run. ${stepType} will record the outcome.`;
  },
  runningDetail: 'A Worker is running it.',
  retryingDetail(at: string): string {
    return at ? `The previous attempt failed. The next starts at ${at}.` : 'The previous attempt failed. The next starts after a backoff.';
  },
  decidingDetail: 'The decision is recorded. The turn picks it up next.',
  outcomeUnknownDetail:
    'It may have run. Check the system it writes to, then tell the agent what happened.',
  tellAgent: 'Tell the agent',
  skippedDetail: 'It did not run.',
  skippedAfterWriteDetail: 'It did not run, because an earlier write of this turn did not succeed.',
  writeQueuedDetail: 'It runs after the earlier writes of this turn succeed.',
  cancelledDetail(reason: string | null | undefined): string {
    if (reason === 'steered') return 'It did not run: a steered message replaced the turn.';
    if (reason === 'turn_cancelled') return 'It did not run: the turn was stopped.';
    return 'It did not run.';
  },
  rejectedDetail(message: string | null | undefined): string {
    return message ? `Rejected. The agent was told: “${message}”` : 'Rejected. It did not run.';
  },
  waitingDetail: 'It waits for a Timer or a remote task.',
  approvedNote: 'Approved.',
  noResultDetail: 'The message text keeps no result for this call.',

  // Approval and question cards.
  approve: 'Approve',
  reject: 'Reject',
  rejectHeading: 'Reject this call',
  rejectReasonLabel: 'Tell the agent why (optional)',
  confirmReject: 'Reject',
  cancel: 'Cancel',
  working: 'Working…',
  answer: 'Answer',
  answerLabel: 'Your answer',
  decline: 'Decline',
  deadline(at: string, remaining: string): string {
    return remaining ? `Rejected automatically at ${at} (in ${remaining}).` : `Rejected automatically at ${at}.`;
  },
  deadlinePassed: 'Its deadline has passed. The session rejects it.',
  alreadyHandled: 'Already handled. Someone else answered this, or its deadline passed. Showing the latest state.',
} as const;
