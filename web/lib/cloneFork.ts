// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { positiveRunAttempt } from './timeTravel';
import type { FlowHistoryEvent } from './types';

/** FlowCloneForkPointType numbers. They differ from the time travel type numbers. */
export const CLONE_FORK_POINT_TYPE = {
  CURRENT: 1,
  BEGINNING: 2,
  HISTORY_EVENT_TIME: 3,
  STEP_TYPE: 4,
  STEP_EXECUTION_ID: 5,
  EVENT_ID: 6,
} as const;

/** IdReusePolicy numbers; 0 leaves the choice to the server default, which disallows reuse. */
export const ID_REUSE_POLICY = {
  SERVER_DEFAULT: 0,
  ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY: 1,
  ALLOW_IF_NO_RUNNING: 2,
  DISALLOW_REUSE: 3,
  ALLOW_TERMINATE_IF_RUNNING: 4,
} as const;

export const idReusePolicyOptions = [
  { value: ID_REUSE_POLICY.SERVER_DEFAULT, label: 'Server default (disallow reuse)' },
  { value: ID_REUSE_POLICY.ALLOW_IF_PREVIOUS_EXISTS_ABNORMALLY, label: 'Allow if the previous run closed abnormally (failed, canceled, or terminated)' },
  { value: ID_REUSE_POLICY.ALLOW_IF_NO_RUNNING, label: 'Allow if no run is running' },
  { value: ID_REUSE_POLICY.DISALLOW_REUSE, label: 'Disallow reuse' },
  { value: ID_REUSE_POLICY.ALLOW_TERMINATE_IF_RUNNING, label: 'Terminate a running run, then start' },
];

/** The source point of a run that CloneForkFlow created, from its start event. */
export interface CloneForkSource {
  flowId: string;
  runId: string;
  runAttempt: number;
  eventId: string;
}

/** Returns the clone fork source of a start event, or null when the run did not start from one. */
export function cloneForkSourceOf(startEvent: FlowHistoryEvent | null): CloneForkSource | null {
  if (startEvent?.type !== 'FlowStartedOrContinued') return null;
  const source = startEvent.payload.cloneForkSource as Record<string, unknown> | undefined;
  if (!source || typeof source.flowId !== 'string' || !source.flowId) return null;
  return {
    flowId: source.flowId,
    runId: typeof source.runId === 'string' ? source.runId : '',
    runAttempt: positiveRunAttempt(source.runAttempt),
    // The API writes int64 values as strings.
    eventId: positiveHistoryEventId(source.eventId),
  };
}

/** Returns a positive event ID from a number or its decimal string, or 0 when it is absent or invalid. */
function positiveHistoryEventId(value: unknown): string {
  if (typeof value === 'string' && /^[1-9]\d*$/.test(value) && BigInt(value) <= 9223372036854775807n) return value;
  if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0) return String(value);
  return '0';
}

/** Suggests a new Flow ID for a clone fork: the source ID, "-fork-", and the time to the second in UTC. */
export function suggestedCloneForkFlowId(sourceFlowId: string, now: Date): string {
  const stamp = now.toISOString().slice(0, 19).replace(/[-:]/g, '').replace('T', '-');
  return `${sourceFlowId}-fork-${stamp}`;
}

/** Returns a new request ID; a resubmit with it is a retry of the same clone fork. */
export function newCloneForkRequestId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  return `clone-fork-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}
