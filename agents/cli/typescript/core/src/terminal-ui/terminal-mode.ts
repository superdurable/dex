// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawnSync } from "node:child_process";
import { appendFileSync, mkdirSync } from "node:fs";
import { dirname } from "node:path";

import { waitForQueuedOutput } from "../queued-output.js";

/** Leave the alternate screen, turn off bracketed paste, and show the cursor. */
const terminalResetSequence = "\u001b[?1049l\u001b[?2004l\u001b[?25h";

/**
 * The launcher's fallback for the terminal: it saves the terminal mode before the terminal UI starts and restores
 * it afterwards, because a terminal UI that crashed or was killed leaves raw mode behind.
 */
export class TerminalModeGuard {
  private readonly savedMode: string | undefined;

  /** Saves the mode of the terminal on stdin with `stty -g`. */
  public constructor() {
    const result = spawnSync("stty", ["-g"], { stdio: ["inherit", "pipe", "ignore"], encoding: "utf8" });
    const mode = result.status === 0 ? result.stdout.trim() : "";
    this.savedMode = mode === "" ? undefined : mode;
  }

  /**
   * Restores the saved mode. After an abnormal end it also leaves the alternate screen, turns off bracketed paste,
   * and shows the cursor.
   * @param isAbnormalEnd - Whether the terminal UI ended without restoring the terminal itself.
   * @param write - Writes to the terminal.
   */
  public restore(isAbnormalEnd: boolean, write: (text: string) => void): void {
    if (this.savedMode !== undefined) {
      spawnSync("stty", [this.savedMode], { stdio: ["inherit", "ignore", "ignore"] });
    }
    if (isAbnormalEnd) {
      write(terminalResetSequence);
    }
  }
}

/**
 * Sends the launcher's own JavaScript writes to stdout and stderr into the log file while the terminal UI owns the
 * terminal, such as a framework's late warning or a `console` call.
 *
 * It does not move fds 1 and 2: Node has no atomic `dup2`, and emulating one races with other threads. The launcher
 * runs only CLI core code, which never writes to those fds directly, and every child it starts during the handoff
 * gets a log file, a pipe, or nothing as its stdout and stderr.
 */
export class LauncherOutputRedirect {
  private readonly originals: { readonly stdout: typeof process.stdout.write; readonly stderr: typeof process.stderr.write };

  /**
   * Starts the redirect, after the output Node still queues for the terminal left.
   * @param logFilePath - The command's log file.
   */
  public static async start(logFilePath: string): Promise<LauncherOutputRedirect> {
    await waitForQueuedOutput([process.stdout, process.stderr]);
    return new LauncherOutputRedirect(logFilePath);
  }

  private constructor(logFilePath: string) {
    this.originals = { stdout: process.stdout.write, stderr: process.stderr.write };
    mkdirSync(dirname(logFilePath), { recursive: true });
    const toLog = ((chunk: string | Uint8Array, encodingOrCallback?: unknown, callback?: unknown): boolean => {
      try {
        appendFileSync(logFilePath, chunk);
      } catch {
        // An unwritable log loses this text; it must still not reach the terminal UI's screen.
      }
      const done = typeof encodingOrCallback === "function" ? encodingOrCallback : callback;
      if (typeof done === "function") {
        (done as (error?: Error | null) => void)();
      }
      return true;
    }) as typeof process.stdout.write;
    process.stdout.write = toLog;
    process.stderr.write = toLog;
  }

  /** Ends the redirect. */
  public close(): void {
    process.stdout.write = this.originals.stdout;
    process.stderr.write = this.originals.stderr;
  }
}
