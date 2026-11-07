// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import { DexAPIError, definitionRevisionHeaders, readResponseJSON } from '@/lib/http';
import { formatElapsedDuration } from '@/lib/timeline';
import type { ActiveStepExecution } from '@/lib/types';
import { SESSION_COPY } from './copy';
import { SESSION_RPC } from './sessionSchema';

export type SessionActionOutcome =
  | { kind: 'done' }
  /** Another operator, a deadline, or the session acted first. Reload and show the result. */
  | { kind: 'already_handled' }
  /** The RPC ran and said no, such as a steer the turn does not accept. */
  | { kind: 'refused'; message: string }
  | { kind: 'failed'; message: string };

export interface SessionActionRequest {
  flowType: string;
  flowId: string;
  action: FlowV2Action;
  input: Record<string, unknown>;
  workQueuePermissions: readonly string[];
  definitionRevision: string;
}

const GRPC_ABORTED = 10;
const RPCS_ANSWERING_ONE_INTERRUPT: readonly string[] = [SESSION_RPC.resolveInterrupt, SESSION_RPC.answerQuestion];

/** Invokes one session Action through POST /api/v2/actions and says what happened. */
export async function invokeSessionAction(
  request: SessionActionRequest,
  fetcher: typeof fetch = fetch,
): Promise<SessionActionOutcome> {
  let output: unknown;
  try {
    const response = await fetcher('/api/v2/actions', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...definitionRevisionHeaders(request.definitionRevision),
      },
      body: JSON.stringify({
        flowType: request.flowType,
        flowId: request.flowId,
        rpcName: request.action.rpcName,
        workQueuePermissions: request.workQueuePermissions,
        input: request.input,
        attributeSnapshot: {},
      }),
    });
    output = (await readResponseJSON<{ output?: unknown }>(response)).output;
  } catch (error) {
    return classifyActionFailure(request.action.rpcName, error);
  }
  if (output !== false) return { kind: 'done' };
  if (RPCS_ANSWERING_ONE_INTERRUPT.includes(request.action.rpcName)) return { kind: 'already_handled' };
  return { kind: 'refused', message: SESSION_COPY.refused(request.action.rpcName) };
}

/** A lock conflict on an interrupt means another resolve holds it: the call is being handled. */
export function classifyActionFailure(rpcName: string, error: unknown): SessionActionOutcome {
  const message = error instanceof Error ? error.message : String(error);
  const isLockConflict = (error instanceof DexAPIError && error.grpcCode === GRPC_ABORTED)
    || /lock/i.test(message);
  if (isLockConflict && RPCS_ANSWERING_ONE_INTERRUPT.includes(rpcName)) return { kind: 'already_handled' };
  return { kind: 'failed', message };
}

/** Input for resolve_interrupt; an empty reason sends none. */
export function resolveInterruptInput(interruptId: string, approved: boolean, reason = ''): Record<string, unknown> {
  const trimmed = reason.trim();
  return {
    interrupt_id: interruptId,
    approved,
    ...(approved || trimmed === '' ? {} : { rejection_message: trimmed }),
  };
}

export interface SessionControlContext {
  isFlowOpen: boolean;
  workQueuePermissions: readonly string[];
  activeSteps: readonly ActiveStepExecution[];
  now: number;
}

/** A pending attempt that waited this long for a Worker means Workers are down. */
export const WORKER_DOWN_AFTER_MS = 15_000;

const WAITING_FOR_WORKER = new Set<unknown>([1, 'STEP_METHOD_PENDING_REASON_WAITING_FOR_WORKER']);

/** Why a session Action, which is an RPC and needs a Worker, cannot run now; null when it can. */
export function actionBlockedReason(
  rpcName: string,
  action: FlowV2Action | undefined,
  context: SessionControlContext,
): string | null {
  if (!action) return SESSION_COPY.noAction(rpcName);
  if (!context.isFlowOpen) return SESSION_COPY.closed;
  if (!context.workQueuePermissions.includes(action.requiredPermission)) {
    return SESSION_COPY.chooseWorkingAs(action.requiredPermission);
  }
  const waited = longestWorkerWaitMs(context.activeSteps, context.now);
  if (waited >= WORKER_DOWN_AFTER_MS) return SESSION_COPY.workersDown(formatElapsedDuration(waited));
  return null;
}

function longestWorkerWaitMs(activeSteps: readonly ActiveStepExecution[], now: number): number {
  let longest = 0;
  for (const step of activeSteps) {
    for (const invocation of [step.waitForInvocation, step.executeInvocation]) {
      if (!invocation || !WAITING_FOR_WORKER.has(invocation.pendingReason)) continue;
      const since = Date.parse(String(invocation.pendingSinceTime ?? ''));
      if (Number.isFinite(since)) longest = Math.max(longest, now - since);
    }
  }
  return longest;
}
