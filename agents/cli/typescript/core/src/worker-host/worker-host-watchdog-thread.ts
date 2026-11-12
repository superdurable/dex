// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// The Worker host's watchdog. It runs in a worker thread with its own event loop, so it keeps its bounds while the
// host's main event loop is stuck.

import { parentPort, workerData } from "node:worker_threads";

import type { WatchdogCommand, WatchdogOptions, WatchdogReport } from "./worker-host-watchdog.js";

const options = workerData as WatchdogOptions;
const port = parentPort;
if (port === null) {
  throw new Error("the watchdog runs only as a worker thread");
}
let isArmed = false;

port.on("message", (command: WatchdogCommand) => {
  if (command.type === "shutdown_started") {
    arm();
  }
});

const parentCheck = setInterval(() => {
  if (process.ppid !== options.launcherPid) {
    clearInterval(parentCheck);
    const report: WatchdogReport = { type: "launcher_gone" };
    port.postMessage(report);
    arm();
  }
}, options.parentCheckIntervalMs);

/**
 * Starts the hard exit bound once: SIGKILL to this host's own process group. It never signals tool process groups,
 * because this thread cannot tell whether the main thread already reaped a group's leader.
 */
function arm(): void {
  if (isArmed) {
    return;
  }
  isArmed = true;
  setTimeout(() => {
    // The host leads its own process group, so this ends it and every descendant left in the group.
    signal(-process.pid, "SIGKILL");
  }, options.hardExitMs);
}

function signal(target: number, name: NodeJS.Signals): void {
  try {
    process.kill(target, name);
  } catch {
    // The group already exited.
  }
}
