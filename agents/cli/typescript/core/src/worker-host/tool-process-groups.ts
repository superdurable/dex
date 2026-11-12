// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

import { readProcessIdentity, signalProcessGroup, type ProcessIdentity } from "./process-identity.js";
import { writeWorkerHostRunRecord } from "./worker-host-run-record.js";

/** How long a tool process group gets between SIGTERM and SIGKILL. */
export const toolProcessGroupTermGraceMs = 2_000;

/** One registered tool process group; release it once its leader is reaped. */
export interface ToolProcessGroupRegistration {
  /** Removes the group from the registry. Call it from the child's `exit` event, after Node reaped the leader. */
  release(): void;
}

/**
 * Where a local tool registers a child that it started in its own process group, so the Worker host can stop the
 * group when it shuts down or when its launcher dies.
 */
export interface ToolProcessGroupRegistry {
  /**
   * Registers a child that leads its own process group, right after it was spawned.
   * @param leaderPid - The child's pid, which is the group's ID.
   * @returns The registration.
   */
  register(leaderPid: number): ToolProcessGroupRegistration;
}

/**
 * The Worker host's registry of tool process groups. It keeps the run record current and tells the watchdog
 * which groups to stop. A leader that is a child of this process and not reaped yet keeps its pid, so signaling the
 * group before the release is safe.
 */
export class RecordedToolProcessGroups implements ToolProcessGroupRegistry {
  private readonly groups = new Map<number, ProcessIdentity>();

  /**
   * Creates the registry.
   * @param recordPath - The Worker host's run record.
   * @param launcher - The launcher's identity.
   * @param host - The Worker host's identity.
   * @param onChange - Receives the registered group IDs after each change.
   */
  public constructor(
    private readonly recordPath: string,
    private readonly launcher: ProcessIdentity,
    private readonly host: ProcessIdentity,
    private readonly onChange: (pgids: readonly number[]) => void,
  ) {
    this.writeRecord();
  }

  public register(leaderPid: number): ToolProcessGroupRegistration {
    const identity = readProcessIdentity(leaderPid) ?? {
      pid: leaderPid,
      pgid: leaderPid,
      start_time: "",
      command_sha256: "",
    };
    this.groups.set(leaderPid, { ...identity, pgid: leaderPid });
    this.changed();
    return {
      release: () => {
        if (this.groups.delete(leaderPid)) {
          this.changed();
        }
      },
    };
  }

  /**
   * Stops every registered group: SIGTERM, up to 2 s for the leaders to be reaped, then SIGKILL to those left.
   * @returns A Promise that resolves after the signals were sent.
   */
  public async terminateAll(): Promise<void> {
    if (this.groups.size === 0) {
      return;
    }
    for (const pgid of this.groups.keys()) {
      signalProcessGroup(pgid, "SIGTERM");
    }
    const deadline = Date.now() + toolProcessGroupTermGraceMs;
    while (this.groups.size > 0 && Date.now() < deadline) {
      await delay(50);
    }
    for (const pgid of this.groups.keys()) {
      signalProcessGroup(pgid, "SIGKILL");
    }
  }

  private changed(): void {
    this.writeRecord();
    this.onChange([...this.groups.keys()]);
  }

  private writeRecord(): void {
    writeWorkerHostRunRecord(this.recordPath, {
      launcher: this.launcher,
      host: this.host,
      tool_groups: [...this.groups.values()],
    });
  }
}
