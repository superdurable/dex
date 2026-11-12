// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { mkdirSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import type { ProcessIdentity } from "./process-identity.js";

/**
 * The record a Worker host keeps in `~/.dex/agents/run/<harness>-<pid>.json` while it runs: its launcher, itself,
 * and the tool process groups it started. A later command uses it to stop what a killed launcher left behind.
 */
export interface WorkerHostRunRecord {
  readonly launcher: ProcessIdentity;
  readonly host: ProcessIdentity;
  readonly tool_groups: readonly ProcessIdentity[];
}

/** Returns `~/.dex/agents/run`. */
export function workerHostRunDirectory(agentsDirectory: string): string {
  return join(agentsDirectory, "run");
}

/** Returns the record path of one Worker host. */
export function workerHostRunRecordPath(agentsDirectory: string, harnessName: string, hostPid: number): string {
  return join(workerHostRunDirectory(agentsDirectory), `${harnessName}-${hostPid}.json`);
}

/**
 * Writes a record synchronously and atomically, so a watchdog or a later command never reads half of it.
 * @param path - The record path.
 * @param record - The record.
 */
export function writeWorkerHostRunRecord(path: string, record: WorkerHostRunRecord): void {
  mkdirSync(join(path, ".."), { recursive: true });
  const temporary = `${path}.${process.pid}.tmp`;
  writeFileSync(temporary, `${JSON.stringify(record)}\n`, { mode: 0o600 });
  renameSync(temporary, path);
}

/**
 * Reads a record, or `undefined` when it is missing or unreadable.
 * @param path - The record path.
 */
export function readWorkerHostRunRecord(path: string): WorkerHostRunRecord | undefined {
  try {
    const record = JSON.parse(readFileSync(path, "utf8")) as Partial<WorkerHostRunRecord>;
    return record.launcher !== undefined && record.host !== undefined && Array.isArray(record.tool_groups)
      ? (record as WorkerHostRunRecord)
      : undefined;
  } catch {
    // A missing or half-written record names no process to stop.
    return undefined;
  }
}

/**
 * Lists the record paths in `~/.dex/agents/run`.
 * @param agentsDirectory - `~/.dex/agents`.
 */
export function listWorkerHostRunRecords(agentsDirectory: string): string[] {
  try {
    return readdirSync(workerHostRunDirectory(agentsDirectory))
      .filter((name) => name.endsWith(".json"))
      .map((name) => join(workerHostRunDirectory(agentsDirectory), name));
  } catch {
    // No run directory: no Worker host ever ran.
    return [];
  }
}

/**
 * Deletes a record.
 * @param path - The record path.
 */
export function removeWorkerHostRunRecord(path: string): void {
  rmSync(path, { force: true });
}
