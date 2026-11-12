// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readProcessIdentity, type ProcessIdentity } from "./process-identity.js";
import { readWorkerHostRunRecord } from "./worker-host-run-record.js";

/** How many tool process groups the watchdog can see at once. */
export const toolGroupSlotCount = 256;

/**
 * The registered tool process groups in shared memory: one Int32 per slot, the group ID or 0. The Worker host's
 * main thread writes a slot synchronously when it registers or releases a group, so the watchdog thread sees a
 * release before it decides to signal, even while the main event loop is stuck.
 */
export type ToolGroupSlots = Int32Array;

/** Returns empty slots in a SharedArrayBuffer. */
export function createToolGroupSlots(): ToolGroupSlots {
  return new Int32Array(new SharedArrayBuffer(Int32Array.BYTES_PER_ELEMENT * toolGroupSlotCount));
}

/**
 * Puts a group in a free slot.
 * @returns The slot, or `undefined` when every slot is taken.
 */
export function occupyToolGroupSlot(slots: ToolGroupSlots, pgid: number): number | undefined {
  for (let index = 0; index < slots.length; index++) {
    if (Atomics.compareExchange(slots, index, 0, pgid) === 0) {
      return index;
    }
  }
  return undefined;
}

/** Empties a slot. */
export function releaseToolGroupSlot(slots: ToolGroupSlots, index: number): void {
  Atomics.store(slots, index, 0);
}

/**
 * Returns the tool process groups the watchdog may signal now: groups still registered in the slots whose record in
 * the run record names a process that still runs with the recorded start time and leads its group.
 *
 * A registered group's leader is a child that Node has not reaped, so its pid cannot name another process; the
 * start time check also refuses a pid that was reaped and reused between the reap and the release. The command line
 * is not compared, because a killed leader that waits to be reaped no longer shows it.
 *
 * @param slots - The shared slots.
 * @param recordPath - The Worker host's run record.
 * @param readIdentity - Reads a process's identity now.
 */
export function toolGroupsSafeToSignal(
  slots: ToolGroupSlots,
  recordPath: string,
  readIdentity: (pid: number) => ProcessIdentity | undefined = readProcessIdentity,
): number[] {
  const registered = new Set<number>();
  for (let index = 0; index < slots.length; index++) {
    const pgid = Atomics.load(slots, index);
    if (pgid !== 0) {
      registered.add(pgid);
    }
  }
  if (registered.size === 0) {
    return [];
  }
  const recorded = readWorkerHostRunRecord(recordPath)?.tool_groups ?? [];
  return recorded
    .filter((group) => registered.has(group.pgid) && group.pid === group.pgid && group.start_time !== "")
    .filter((group) => {
      const current = readIdentity(group.pid);
      return current !== undefined && current.start_time === group.start_time && current.pgid === group.pgid;
    })
    .map((group) => group.pgid);
}
