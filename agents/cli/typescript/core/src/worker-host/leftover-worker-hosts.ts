// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

import { isSameProcess, isSameProcessGroupLeader, signalProcessGroup, type ProcessIdentity } from "./process-identity.js";
import { toolProcessGroupTermGraceMs } from "./tool-process-groups.js";
import {
  listWorkerHostRunRecords,
  readWorkerHostRunRecord,
  removeWorkerHostRunRecord,
  workerHostRunRecordPath,
} from "./worker-host-run-record.js";

/** What one cleanup stopped. */
export interface StoppedGroups {
  readonly isHostStopped: boolean;
  readonly toolGroupCount: number;
}

/**
 * Stops what Worker hosts of killed launchers left behind, from `~/.dex/agents/run`. A record counts when its
 * launcher no longer runs. Before each signal, the process must still be the recorded one and lead the recorded
 * group (for a tool group, without its command line); otherwise nothing is signaled and only the stale record goes.
 *
 * @param agentsDirectory - `~/.dex/agents`.
 * @param log - Writes one line for each Worker host it stopped.
 */
export async function cleanUpLeftoverWorkerHosts(agentsDirectory: string, log: (line: string) => void): Promise<void> {
  for (const path of listWorkerHostRunRecords(agentsDirectory)) {
    const record = readWorkerHostRunRecord(path);
    if (record !== undefined && isSameProcess(record.launcher)) {
      continue;
    }
    const stopped =
      record === undefined
        ? { isHostStopped: false, toolGroupCount: 0 }
        : await stopRecordedGroups([record.host, ...record.tool_groups], record.host);
    removeWorkerHostRunRecord(path);
    if (stopped.isHostStopped || stopped.toolGroupCount > 0) {
      const groups = stopped.toolGroupCount === 1 ? "1 tool process group" : `${stopped.toolGroupCount} tool process groups`;
      log(
        stopped.isHostStopped
          ? `Stopped a leftover worker host (pid ${record?.host.pid ?? 0}) and ${groups}.`
          : `Stopped ${groups} that a leftover worker host left behind.`,
      );
    }
  }
}

/**
 * Stops what one Worker host's record still lists after the launcher stopped that host, and deletes the record.
 * @param agentsDirectory - `~/.dex/agents`.
 * @param harnessName - The harness name.
 * @param hostPid - The Worker host's pid.
 */
export async function cleanUpWorkerHostRecord(agentsDirectory: string, harnessName: string, hostPid: number): Promise<void> {
  const path = workerHostRunRecordPath(agentsDirectory, harnessName, hostPid);
  const record = readWorkerHostRunRecord(path);
  if (record !== undefined) {
    await stopRecordedGroups([record.host, ...record.tool_groups], record.host);
  }
  removeWorkerHostRunRecord(path);
}

/** The process checks and signals of leftover cleanup; tests replace them. */
export interface LeftoverProcessControl {
  /** Whether the recorded process still leads its group, with or without its command line. */
  isLeader(group: ProcessIdentity, isCommandChecked: boolean): boolean;
  /** Sends a signal to a process group. */
  signal(pgid: number, signal: NodeJS.Signals): void;
  /** Waits between SIGTERM and SIGKILL. */
  waitForGrace(): Promise<void>;
}

const systemProcessControl: LeftoverProcessControl = {
  isLeader: (group, isCommandChecked) => isSameProcessGroupLeader(group, isCommandChecked),
  signal: (pgid, signal) => signalProcessGroup(pgid, signal),
  waitForGrace: () => delay(toolProcessGroupTermGraceMs),
};

/**
 * Sends SIGTERM to each group whose leader still checks out, checking each one right before its own signal, because
 * signaling an earlier group can end a later group's leader. 2 s later it sends SIGKILL to each signaled group whose
 * leader still checks out. The Worker host's command line is part of its identity; a tool group's is not, because
 * its shell may exec the command.
 * @param groups - The Worker host first, then its tool groups.
 * @param host - The Worker host's identity.
 * @param control - The process checks and signals.
 * @returns What it stopped.
 */
export async function stopRecordedGroups(
  groups: readonly ProcessIdentity[],
  host: ProcessIdentity,
  control: LeftoverProcessControl = systemProcessControl,
): Promise<StoppedGroups> {
  const isLeader = (group: ProcessIdentity): boolean => control.isLeader(group, group === host);
  const signaled: ProcessIdentity[] = [];
  for (const group of groups) {
    if (isLeader(group)) {
      control.signal(group.pgid, "SIGTERM");
      signaled.push(group);
    }
  }
  if (signaled.length === 0) {
    return { isHostStopped: false, toolGroupCount: 0 };
  }
  await control.waitForGrace();
  for (const group of signaled) {
    if (isLeader(group)) {
      control.signal(group.pgid, "SIGKILL");
    }
  }
  const isHostStopped = signaled.includes(host);
  return { isHostStopped, toolGroupCount: signaled.length - (isHostStopped ? 1 : 0) };
}
