// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { closeSync, openSync, readFileSync, statSync } from "node:fs";
import { setTimeout as delay } from "node:timers/promises";

const readyPollIntervalMs = 50;
const gracefulStopTimeoutMs = 15_000;
const runningProcessGroups = new Set<ChildProcessGroup>();
let isExitCleanupRegistered = false;

/** Command, environment, and readiness of one child process group. */
export interface ChildProcessGroupLaunch {
  readonly description: string;
  readonly command: string;
  readonly commandArguments: readonly string[];
  readonly environment: NodeJS.ProcessEnv;
  readonly logPath: string;
  readonly readyLine: string;
  readonly readyTimeoutMs: number;
}

/** A child process that leads its own process group, so signals reach every descendant. */
export class ChildProcessGroup {
  private readonly exited: Promise<void>;
  private hasExited = false;
  private spawnError: Error | undefined;

  private constructor(
    private readonly launch: ChildProcessGroupLaunch,
    private readonly child: ChildProcess,
    private readonly logOffset: number,
  ) {
    this.exited = new Promise((resolve) => {
      const markExited = (): void => {
        this.hasExited = true;
        runningProcessGroups.delete(this);
        resolve();
      };
      child.once("exit", markExited);
      child.once("error", (error) => {
        this.spawnError = error;
        // A process that never spawned emits no exit event.
        if (child.pid === undefined) {
          markExited();
        }
      });
    });
  }

  public static async start(launch: ChildProcessGroupLaunch): Promise<ChildProcessGroup> {
    registerExitCleanup();
    const logOffset = fileSize(launch.logPath);
    const logDescriptor = openSync(launch.logPath, "a");
    let child: ChildProcess;
    try {
      child = spawn(launch.command, [...launch.commandArguments], {
        detached: true,
        env: launch.environment,
        stdio: ["ignore", logDescriptor, logDescriptor],
      });
    } finally {
      closeSync(logDescriptor);
    }
    const processGroup = new ChildProcessGroup(launch, child, logOffset);
    runningProcessGroups.add(processGroup);
    try {
      await processGroup.waitForReadyLine();
    } catch (error: unknown) {
      await processGroup.kill();
      throw error;
    }
    return processGroup;
  }

  /** Process ID of the group leader. */
  public get pid(): number {
    const pid = this.child.pid;
    if (pid === undefined) {
      throw new Error(`${this.launch.description} has no process ID`);
    }
    return pid;
  }

  public get isRunning(): boolean {
    return !this.hasExited;
  }

  /** Output written since this process started. */
  public readLog(): string {
    return readFileSync(this.launch.logPath).subarray(this.logOffset).toString("utf8");
  }

  /** Sends SIGKILL to the whole group and waits for the leader to exit. */
  public async kill(): Promise<void> {
    this.signalGroup("SIGKILL");
    await this.exited;
  }

  /** Sends SIGTERM to the whole group, then SIGKILL after 15 seconds. */
  public async stop(): Promise<void> {
    this.signalGroup("SIGTERM");
    const gracefulStopTimer = new AbortController();
    const isStopped = await Promise.race([
      this.exited.then(() => true),
      delay(gracefulStopTimeoutMs, false, { signal: gracefulStopTimer.signal }).catch(() => true),
    ]);
    gracefulStopTimer.abort();
    if (!isStopped) {
      await this.kill();
    }
  }

  public signalGroupSynchronously(signal: NodeJS.Signals): void {
    this.signalGroup(signal);
  }

  private signalGroup(signal: NodeJS.Signals): void {
    if (this.hasExited || this.child.pid === undefined) {
      return;
    }
    try {
      process.kill(-this.child.pid, signal);
    } catch (error: unknown) {
      if (!isNoSuchProcessError(error)) {
        throw error;
      }
    }
  }

  private async waitForReadyLine(): Promise<void> {
    const deadline = Date.now() + this.launch.readyTimeoutMs;
    while (Date.now() < deadline) {
      const spawnError = this.spawnError;
      if (spawnError !== undefined) {
        throw new Error(`${this.launch.description} failed to start: ${spawnError.message}`, {
          cause: spawnError,
        });
      }
      if (this.readLog().includes(this.launch.readyLine)) {
        return;
      }
      if (this.hasExited) {
        throw new Error(`${this.launch.description} exited before it was ready:\n${this.readLog()}`);
      }
      await delay(readyPollIntervalMs);
    }
    throw new Error(`${this.launch.description} was not ready within ${this.launch.readyTimeoutMs} ms:\n${this.readLog()}`);
  }
}

function registerExitCleanup(): void {
  if (isExitCleanupRegistered) {
    return;
  }
  isExitCleanupRegistered = true;
  // Never leave an engine or Worker behind when the test process exits without cleanup.
  process.once("exit", () => {
    for (const processGroup of runningProcessGroups) {
      processGroup.signalGroupSynchronously("SIGKILL");
    }
  });
}

function fileSize(path: string): number {
  try {
    return statSync(path).size;
  } catch (error: unknown) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") {
      return 0;
    }
    throw error;
  }
}

function isNoSuchProcessError(error: unknown): boolean {
  return error instanceof Error && "code" in error && error.code === "ESRCH";
}
