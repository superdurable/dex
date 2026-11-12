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
 * terminal, such as a framework's late warning. Bytes written to the file descriptors directly are not covered.
 */
export class LauncherOutputRedirect {
  private readonly originals: { readonly stdout: typeof process.stdout.write; readonly stderr: typeof process.stderr.write };

  /**
   * Starts the redirect.
   * @param logFilePath - The command's log file.
   */
  public constructor(logFilePath: string) {
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

  /** Writes to the terminal, bypassing the redirect. */
  public writeToTerminal(text: string): void {
    this.originals.stderr.call(process.stderr, text);
  }

  /** Ends the redirect. */
  public close(): void {
    process.stdout.write = this.originals.stdout;
    process.stderr.write = this.originals.stderr;
  }
}
