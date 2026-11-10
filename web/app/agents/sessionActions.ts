// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import { DexAPIError, definitionRevisionHeaders, readResponseJSON } from '@/lib/http';
import { SESSION_COPY } from './copy';
import { SESSION_RPC } from './sessionSchema';
import { webURL } from '@/lib/basePath';

export type SessionActionOutcome =
  | { kind: 'done' }
  /** The interrupt RPC returned false: another operator or a deadline acted first. Reload and show the result. */
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
const GRPC_UNAVAILABLE = 14;
const RPCS_ANSWERING_ONE_INTERRUPT: readonly string[] = [SESSION_RPC.resolveInterrupt, SESSION_RPC.answerQuestion];

/** Waits before each retry of an RPC that could not take its locks; the RPC committed nothing. */
export const LOCK_CONFLICT_RETRY_DELAYS_MS: readonly number[] = [200, 500, 1000];

/**
 * Invokes one session Action through POST /api/v2/actions and says what happened.
 *
 * A lock conflict is retried after each delay in `retryDelaysMs`, then reported as busy.
 */
export async function invokeSessionAction(
  request: SessionActionRequest,
  fetcher: typeof fetch = fetch,
  retryDelaysMs: readonly number[] = LOCK_CONFLICT_RETRY_DELAYS_MS,
): Promise<SessionActionOutcome> {
  for (let attempt = 0; ; attempt += 1) {
    let output: unknown;
    try {
      output = await postSessionAction(request, fetcher);
    } catch (error) {
      if (isLockConflict(error) && attempt < retryDelaysMs.length) {
        await new Promise((resolve) => setTimeout(resolve, retryDelaysMs[attempt]));
        continue;
      }
      return classifyActionFailure(error);
    }
    if (output !== false) return { kind: 'done' };
    if (RPCS_ANSWERING_ONE_INTERRUPT.includes(request.action.rpcName)) return { kind: 'already_handled' };
    return { kind: 'refused', message: SESSION_COPY.refused(request.action.rpcName) };
  }
}

async function postSessionAction(request: SessionActionRequest, fetcher: typeof fetch): Promise<unknown> {
  const response = await fetcher(webURL('/api/v2/actions'), {
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
  return (await readResponseJSON<{ output?: unknown }>(response)).output;
}

/** The RPC could not take its Attribute locks, so it committed nothing and is safe to retry. */
function isLockConflict(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return (error instanceof DexAPIError && error.grpcCode === GRPC_ABORTED) || /lock/i.test(message);
}

/** A lock conflict left the call as it was; only a false result means someone else answered. */
export function classifyActionFailure(error: unknown): SessionActionOutcome {
  const message = error instanceof Error ? error.message : String(error);
  if (isLockConflict(error)) return { kind: 'failed', message: SESSION_COPY.busy };
  // The Server answers UNAVAILABLE when an RPC dispatch finds no Worker or loses it.
  if (error instanceof DexAPIError && error.grpcCode === GRPC_UNAVAILABLE) return { kind: 'failed', message: SESSION_COPY.noWorker };
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
}

/** Why a session Action cannot run now, or null when it can. The Step queue never blocks it. */
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
  return null;
}
