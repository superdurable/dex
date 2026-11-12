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
let toolGroups: readonly number[] = [];
let isArmed = false;

port.on("message", (command: WatchdogCommand) => {
  if (command.type === "tool_groups") {
    toolGroups = command.pgids;
  } else {
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

/** Starts the hard exit bound once: tool groups first, then this host's own process group. */
function arm(): void {
  if (isArmed) {
    return;
  }
  isArmed = true;
  setTimeout(() => {
    for (const pgid of toolGroups) {
      signal(-pgid, "SIGTERM");
    }
    setTimeout(() => {
      for (const pgid of toolGroups) {
        signal(-pgid, "SIGKILL");
      }
      // The host leads its own process group, so this ends it and every descendant left in the group.
      signal(-process.pid, "SIGKILL");
    }, toolGroups.length === 0 ? 0 : options.toolGroupTermGraceMs);
  }, options.hardExitMs);
}

function signal(target: number, name: NodeJS.Signals): void {
  try {
    process.kill(target, name);
  } catch {
    // The group already exited.
  }
}
