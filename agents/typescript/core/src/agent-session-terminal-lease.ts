// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AcquireSessionTerminalLeaseRequest,
  SessionTerminalLease,
  SessionTerminalLeaseResult,
} from "./agent-session-values.js";

/** The longest `ttlSeconds` an acquire accepts. */
export const sessionTerminalLeaseMaxTtlSeconds = 3_600;
/** The most code points a lease's `hostName` may have. */
export const sessionTerminalLeaseMaxHostNameLength = 255;
/** Bounds each lease RPC, so a paused Worker holds the lease's lock for 5 s at most. */
export const sessionTerminalLeaseRpcTimeoutMs = 5_000;

/** The outcome of one acquire: the result to return and the lease to write, or `null` to write nothing. */
export interface SessionTerminalLeaseAcquisition {
  readonly result: SessionTerminalLeaseResult;
  readonly leaseToWrite: SessionTerminalLease | null;
}

/**
 * Decides one `acquire_session_terminal_lease` call from the stored lease and the Worker's clock.
 *
 * @param current - The stored lease, or `undefined` when no terminal holds the session.
 * @param request - The RPC input.
 * @param now - The Worker's current time.
 * @returns The RPC result and the lease to write: the caller's when acquired, `null` when refused or invalid.
 */
export function decideSessionTerminalLeaseAcquisition(
  current: SessionTerminalLease | undefined,
  request: AcquireSessionTerminalLeaseRequest,
  now: Date,
): SessionTerminalLeaseAcquisition {
  if (!isValidLeaseRequest(request)) {
    return { result: { isAcquired: false, lease: null }, leaseToWrite: null };
  }
  const isSameHolder = current !== undefined && current.holderId === request.holderId;
  if (current !== undefined && !isSameHolder && !isLeaseExpired(current, now)) {
    return { result: { isAcquired: false, lease: current }, leaseToWrite: null };
  }
  const lease: SessionTerminalLease = {
    holderId: request.holderId,
    hostName: request.hostName,
    processId: request.processId,
    openedAt: isSameHolder ? current.openedAt : now.toISOString(),
    expiresAt: new Date(now.getTime() + request.ttlSeconds * 1_000).toISOString(),
  };
  return { result: { isAcquired: true, lease }, leaseToWrite: lease };
}

function isValidLeaseRequest(request: AcquireSessionTerminalLeaseRequest): boolean {
  return (
    request.holderId.trim() !== "" &&
    [...request.hostName].length <= sessionTerminalLeaseMaxHostNameLength &&
    request.processId >= 0 &&
    request.ttlSeconds >= 1 &&
    request.ttlSeconds <= sessionTerminalLeaseMaxTtlSeconds
  );
}

// An expiry that does not parse counts as expired, so a damaged lease never blocks a session.
function isLeaseExpired(lease: SessionTerminalLease, now: Date): boolean {
  const expiresAt = Date.parse(lease.expiresAt);
  return Number.isNaN(expiresAt) || expiresAt <= now.getTime();
}
