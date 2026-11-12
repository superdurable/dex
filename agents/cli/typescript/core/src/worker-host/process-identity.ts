// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";

/** What identifies one process across time: its group, start time, and command line. */
export interface ProcessIdentity {
  readonly pid: number;
  readonly pgid: number;
  /** The start time as `ps -o lstart` prints it in the C locale. */
  readonly start_time: string;
  /** The SHA-256 of the command line, so a record never holds a secret that a shell command carries. */
  readonly command_sha256: string;
}

/**
 * Reads a process's identity with `ps`, which darwin and Linux both have.
 * @param pid - The process.
 * @returns Its identity, or `undefined` when no such process exists.
 */
export function readProcessIdentity(pid: number): ProcessIdentity | undefined {
  if (!Number.isSafeInteger(pid) || pid <= 0) {
    return undefined;
  }
  let output: string;
  try {
    output = execFileSync("ps", ["-ww", "-o", "pgid=", "-o", "lstart=", "-o", "command=", "-p", String(pid)], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
      env: { ...process.env, LC_ALL: "C" },
    });
  } catch {
    // ps exits 1 when the process does not exist.
    return undefined;
  }
  const fields = output.trim().split(/\s+/);
  const pgid = Number(fields[0]);
  if (fields.length < 7 || !Number.isSafeInteger(pgid)) {
    return undefined;
  }
  const command = fields.slice(6).join(" ");
  return {
    pid,
    pgid,
    start_time: fields.slice(1, 6).join(" "),
    command_sha256: createHash("sha256").update(command, "utf8").digest("hex"),
  };
}

/**
 * Returns whether a process still is the recorded one: it exists with the recorded start time and command line.
 * @param recorded - The identity recorded when it started.
 */
export function isSameProcess(recorded: ProcessIdentity): boolean {
  const current = readProcessIdentity(recorded.pid);
  return current !== undefined && current.start_time === recorded.start_time && current.command_sha256 === recorded.command_sha256;
}

/**
 * Returns whether a process still is the recorded one and leads its recorded group: it exists, its start time and
 * command line match, its group is the recorded group, and its pid is that group's ID.
 * @param recorded - The identity recorded when it started.
 */
export function isSameProcessGroupLeader(recorded: ProcessIdentity): boolean {
  const current = readProcessIdentity(recorded.pid);
  return (
    current !== undefined &&
    current.start_time === recorded.start_time &&
    current.command_sha256 === recorded.command_sha256 &&
    current.pgid === recorded.pgid &&
    current.pid === current.pgid
  );
}

/**
 * Sends a signal to a process group, ignoring a group that is already gone.
 * @param pgid - The group.
 * @param signal - The signal.
 */
export function signalProcessGroup(pgid: number, signal: NodeJS.Signals): void {
  try {
    process.kill(-pgid, signal);
  } catch {
    // The group already exited.
  }
}
