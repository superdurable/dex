// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Worker } from "node:worker_threads";


/** How often the watchdog checks that the launcher is still the parent. */
const parentCheckIntervalMs = 1_000;
/** How long an orderly shutdown may take after the launcher died or asked for it. */
export const workerHostHardExitMs = 15_000;

/** What the watchdog thread starts with. */
export interface WatchdogOptions {
  readonly launcherPid: number;
  readonly parentCheckIntervalMs: number;
  readonly hardExitMs: number;
}

/** A message from the host to its watchdog. */
export type WatchdogCommand = { readonly type: "shutdown_started" };

/** A message from the watchdog to the host. */
export type WatchdogReport = { readonly type: "launcher_gone" };

/**
 * The Worker host's parent-death watchdog, in a worker thread. It checks every second that the launcher is still
 * the parent. Once the launcher is gone, or an orderly shutdown starts, it allows 15 s; then it kills the host's own
 * process group. It never signals tool process groups: the main thread stops them in an orderly shutdown.
 */
export class WorkerHostWatchdog {
  private readonly thread: Worker;

  /**
   * Starts the watchdog.
   * @param launcherPid - The launcher's pid.
   * @param onLauncherGone - Called on the main loop when the watchdog finds the launcher gone.
   */
  public constructor(launcherPid: number, onLauncherGone: () => void) {
    const options: WatchdogOptions = {
      launcherPid,
      parentCheckIntervalMs,
      hardExitMs: workerHostHardExitMs,
    };
    this.thread = new Worker(new URL("./worker-host-watchdog-thread.js", import.meta.url), { workerData: options });
    this.thread.on("message", (report: WatchdogReport) => {
      if (report.type === "launcher_gone") {
        onLauncherGone();
      }
    });
    this.thread.unref();
  }

  /** Starts the hard bound of an orderly shutdown. */
  public shutdownStarted(): void {
    this.post({ type: "shutdown_started" });
  }

  private post(command: WatchdogCommand): void {
    this.thread.postMessage(command);
  }
}
