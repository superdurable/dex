// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ChildProcess } from "node:child_process";
import { setTimeout as delay } from "node:timers/promises";

import { readProcessIdentity, signalProcessGroup, type ProcessIdentity } from "./process-identity.js";
import { writeWorkerHostRunRecord } from "./worker-host-run-record.js";

/** How long a tool process group gets between SIGTERM and SIGKILL. */
export const toolProcessGroupTermGraceMs = 2_000;

/** One registered tool process group; release it once its leader exited. */
export interface ToolProcessGroupRegistration {
  /** Removes the group from the registry. Call it from the child's `exit` event. */
  release(): void;
}

/**
 * Where a local tool registers a child that it started in its own process group, so the Worker host can stop the
 * group when it shuts down.
 */
export interface ToolProcessGroupRegistry {
  /**
   * Registers a child that leads its own process group, right after it was spawned.
   * @param leader - The child, whose pid is the group's ID.
   * @returns The registration.
   */
  register(leader: ChildProcess): ToolProcessGroupRegistration;
}

/** One registered group: its leader and the identity the run record holds. */
interface RegisteredToolGroup {
  readonly leader: ChildProcess;
  readonly identity: ProcessIdentity;
}

/**
 * The Worker host's registry of tool process groups, which keeps the run record current.
 *
 * Only the main thread signals a group, and only while its leader's `ChildProcess` has neither emitted `exit` nor
 * set `exitCode` or `signalCode`. Node reaps a child on the main event loop and reports it there, so until then the
 * leader's pid cannot name another process. When the main loop is stuck nothing signals the groups; the run record
 * stays, and the next command start's leftover cleanup stops them.
 */
export class RecordedToolProcessGroups implements ToolProcessGroupRegistry {
  private readonly groups = new Map<number, RegisteredToolGroup>();

  /**
   * Creates the registry.
   * @param recordPath - The Worker host's run record.
   * @param launcher - The launcher's identity.
   * @param host - The Worker host's identity.
   */
  public constructor(
    private readonly recordPath: string,
    private readonly launcher: ProcessIdentity,
    private readonly host: ProcessIdentity,
  ) {
    this.writeRecord();
  }

  public register(leader: ChildProcess): ToolProcessGroupRegistration {
    const leaderPid = leader.pid;
    if (leaderPid === undefined) {
      return { release: () => undefined };
    }
    const identity = readProcessIdentity(leaderPid) ?? {
      pid: leaderPid,
      pgid: leaderPid,
      start_time: "",
      command_sha256: "",
    };
    this.groups.set(leaderPid, { leader, identity: { ...identity, pgid: leaderPid } });
    this.writeRecord();
    return {
      release: () => {
        if (this.groups.delete(leaderPid)) {
          this.writeRecord();
        }
      },
    };
  }

  /**
   * Stops every registered group whose leader has not exited: SIGTERM, up to 2 s for the leaders to exit, then
   * SIGKILL to those left.
   * @returns A Promise that resolves after the signals were sent.
   */
  public async terminateAll(): Promise<void> {
    if (this.groups.size === 0) {
      return;
    }
    this.signalRunningGroups("SIGTERM");
    const deadline = Date.now() + toolProcessGroupTermGraceMs;
    while (this.groups.size > 0 && Date.now() < deadline) {
      await delay(50);
    }
    this.signalRunningGroups("SIGKILL");
  }

  private signalRunningGroups(signal: NodeJS.Signals): void {
    for (const [pgid, group] of this.groups) {
      if (group.leader.exitCode === null && group.leader.signalCode === null) {
        signalProcessGroup(pgid, signal);
      }
    }
  }

  private writeRecord(): void {
    writeWorkerHostRunRecord(this.recordPath, {
      launcher: this.launcher,
      host: this.host,
      tool_groups: [...this.groups.values()].map((group) => group.identity),
    });
  }
}
