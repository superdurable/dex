// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { DexAPIError } from '@/lib/http';
import { FLOW_STATUS } from '@/lib/types';

/**
 * An empty answer, a failed refresh over a good answer, and never having asked are
 * three different things. Collapsing them sends a reader to the wrong place.
 */
export type Liveness = 'loading' | 'ok' | 'stale' | 'unreachable' | 'stranded';

export interface Held<T> {
  readonly value: T | null;
  readonly liveness: Liveness;
  readonly reason: string | null;
}

export type ReadOutcome<T> =
  | { readonly state: 'ok'; readonly value: T }
  | { readonly state: 'unreachable'; readonly reason: string }
  | { readonly state: 'stranded'; readonly reason: string };

export function nothingHeld<T>(): Held<T> {
  return { value: null, liveness: 'loading', reason: null };
}

export function absorb<T>(prior: Held<T>, next: ReadOutcome<T>): Held<T> {
  if (next.state === 'ok') return { value: next.value, liveness: 'ok', reason: null };
  // A stranded run keeps its own liveness: nothing is coming back, so it is not stale.
  if (next.state === 'stranded') {
    return { value: prior.value, liveness: 'stranded', reason: next.reason };
  }
  // Showing a true-a-moment-ago answer late beats blanking it; reporting it as fresh is worse.
  if (prior.value !== null) return { value: prior.value, liveness: 'stale', reason: next.reason };
  return { value: null, liveness: 'unreachable', reason: next.reason };
}

export function readFailureReason(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/**
 * Dex routes a Flow RPC to the worker that owns the run, so a worker that exited makes
 * one run permanently unreadable while the server stays healthy.
 *
 * Measured, not assumed: that surfaces as FailedPrecondition, which the same code also
 * uses for "Flow is not active". A closed run is therefore excluded by status — its
 * Display is equally unreadable, but nothing is owed on it.
 */
const GRPC_FAILED_PRECONDITION = 9;
const INACTIVE_FLOW_MESSAGE = 'Flow is not active';

export function isStrandedRunFailure(error: unknown, flowStatusCode: number | undefined): boolean {
  if (!(error instanceof DexAPIError)) return false;
  if (error.grpcCode !== GRPC_FAILED_PRECONDITION) return false;
  if (error.message.trim() === INACTIVE_FLOW_MESSAGE) return false;
  return isOpenFlowStatusCode(flowStatusCode);
}

export function classifyReadFailure<T>(
  error: unknown,
  flowStatusCode: number | undefined,
): ReadOutcome<T> {
  const reason = readFailureReason(error);
  return isStrandedRunFailure(error, flowStatusCode)
    ? { state: 'stranded', reason }
    : { state: 'unreachable', reason };
}

/** Running is the only non-terminal status. Gate on the code: the Go and TS labels disagree. */
export const RUNNING_FLOW_STATUS_CODE = 1;

export function isOpenFlowStatusCode(flowStatusCode: number | undefined): boolean {
  return flowStatusCode === RUNNING_FLOW_STATUS_CODE;
}

export function openFlowStatusLabel(): string {
  return FLOW_STATUS[RUNNING_FLOW_STATUS_CODE];
}
