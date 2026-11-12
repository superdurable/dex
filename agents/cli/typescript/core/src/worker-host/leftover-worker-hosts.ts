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
interface StoppedGroups {
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
        : await stopGroups([record.host, ...record.tool_groups], record.host);
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
    await stopGroups([record.host, ...record.tool_groups], record.host);
  }
  removeWorkerHostRunRecord(path);
}

/**
 * Sends SIGTERM to each group whose leader still checks out, then SIGKILL 2 s later after checking again. The Worker
 * host's command line is part of its identity; a tool group's is not, because its shell may exec the command.
 */
async function stopGroups(groups: readonly ProcessIdentity[], host: ProcessIdentity): Promise<StoppedGroups> {
  const isLeader = (group: ProcessIdentity): boolean => isSameProcessGroupLeader(group, group === host);
  const signaled = groups.filter((group) => isLeader(group));
  if (signaled.length === 0) {
    return { isHostStopped: false, toolGroupCount: 0 };
  }
  for (const group of signaled) {
    signalProcessGroup(group.pgid, "SIGTERM");
  }
  await delay(toolProcessGroupTermGraceMs);
  for (const group of signaled) {
    if (isLeader(group)) {
      signalProcessGroup(group.pgid, "SIGKILL");
    }
  }
  const isHostStopped = signaled.includes(host);
  return { isHostStopped, toolGroupCount: signaled.length - (isHostStopped ? 1 : 0) };
}
