// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawnSync } from "node:child_process";
import { appendFileSync, closeSync, constants, mkdirSync, openSync } from "node:fs";
import { dirname } from "node:path";
import { isatty } from "node:tty";

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

/** The fd of stdout or stderr. */
type OutputFd = 1 | 2;

/** What the terminal UI gets as its stdout or stderr: a copy of the terminal's fd, or the launcher's own fd. */
export type TerminalOutput = number | "inherit";

/**
 * Sends the launcher's own output into the log file while the terminal UI owns the terminal, and back afterwards.
 *
 * Fds 1 and 2 point at the log, so bytes written to them directly, such as a library's, and children the launcher
 * starts meanwhile with inherited stdio land there too. JavaScript writes to `process.stdout` and `process.stderr`
 * go to the log as well: a terminal stream may write through its own reopened fd. An fd that is not a terminal,
 * such as a file the user redirected stderr to, stays as it is.
 */
export class LauncherOutputRedirect {
  /** The terminal UI's stdout and stderr: copies of the terminal's fds 1 and 2. */
  public readonly terminalOutput: readonly [TerminalOutput, TerminalOutput];
  private readonly originals: { readonly stdout: typeof process.stdout.write; readonly stderr: typeof process.stderr.write };
  /** The copy of the terminal's fd for each fd that now points at the log. */
  private readonly terminalCopies: ReadonlyMap<OutputFd, number>;

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
    this.terminalCopies = moveTerminalOutputToLog(logFilePath);
    this.terminalOutput = [this.terminalCopies.get(1) ?? "inherit", this.terminalCopies.get(2) ?? "inherit"];
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

  /** Ends the redirect: fds 1 and 2 point at the terminal again. */
  public close(): void {
    process.stdout.write = this.originals.stdout;
    process.stderr.write = this.originals.stderr;
    restoreTerminalOutput(this.terminalCopies);
  }
}

/**
 * Points each of fds 1 and 2 that is a terminal at the log.
 * @param logFilePath - The command's log file.
 * @returns A copy of the terminal's fd for each fd it moved; none when it could not move them.
 */
function moveTerminalOutputToLog(logFilePath: string): ReadonlyMap<OutputFd, number> {
  const copies = new Map<OutputFd, number>();
  let logFd: number | undefined;
  try {
    for (const fd of [1, 2] as const) {
      if (isatty(fd)) {
        copies.set(fd, openSync(`/dev/fd/${fd}`, constants.O_WRONLY));
      }
    }
    logFd = openSync(logFilePath, constants.O_WRONLY | constants.O_APPEND | constants.O_CREAT, 0o600);
    for (const fd of copies.keys()) {
      duplicateOnto(logFd, fd, constants.O_WRONLY | constants.O_APPEND);
    }
    return copies;
  } catch (error: unknown) {
    restoreTerminalOutput(copies);
    try {
      appendFileSync(logFilePath, `The launcher's output stays on the terminal: ${String(error)}\n`);
    } catch {
      // The log is unwritable; only JavaScript writes are kept off the terminal then.
    }
    return new Map();
  } finally {
    if (logFd !== undefined) {
      closeSync(logFd);
    }
  }
}

/**
 * Points fds 1 and 2 back at the terminal and closes the copies. A terminal that hung up may not reopen; that fd
 * then keeps the log.
 * @param copies - The copy of the terminal's fd for each moved fd.
 */
function restoreTerminalOutput(copies: ReadonlyMap<OutputFd, number>): void {
  for (const [fd, copy] of copies) {
    try {
      duplicateOnto(copy, fd, constants.O_WRONLY);
    } catch {
      // The log keeps this fd, which is still better than a closed stdout.
    }
    closeSync(copy);
  }
}

/**
 * Points `target` at what `source` refers to, as `dup2` does: Node has no `dup2`, but opening `/dev/fd/<source>`
 * duplicates or reopens it, and `open` returns the lowest free fd. `target` stays open if opening fails.
 * @param source - The fd to duplicate.
 * @param target - The fd to replace.
 * @param flags - The open flags, which must fit `source`.
 * @throws {Error} When `/dev/fd/<source>` cannot be opened, or another thread took `target` meanwhile.
 */
function duplicateOnto(source: number, target: number, flags: number): void {
  // Opening first proves it works before `target` closes.
  closeSync(openSync(`/dev/fd/${source}`, flags));
  closeSync(target);
  const lowerFds: number[] = [];
  try {
    for (;;) {
      const opened = openSync(`/dev/fd/${source}`, flags);
      if (opened === target) {
        return;
      }
      if (opened > target) {
        closeSync(opened);
        throw new Error(`fd ${target} was taken while the launcher moved it`);
      }
      lowerFds.push(opened);
    }
  } finally {
    for (const fd of lowerFds) {
      closeSync(fd);
    }
  }
}
