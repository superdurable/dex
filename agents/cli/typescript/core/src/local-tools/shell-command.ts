// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";

import { ToolExecutionError } from "@superdurable/dex-agents";

import type { ToolProcessGroupRegistry } from "../worker-host/tool-process-groups.js";

/** Seconds a shell command may run when the call sets no timeout. */
export const defaultShellTimeoutSeconds = 120;
/** The longest timeout a call may set, in seconds. */
export const maximumShellTimeoutSeconds = 600;
const maximumOutputBytes = 64 * 1024;

/**
 * Runs one command with the system shell in a directory and returns its exit status and output.
 *
 * The command runs in its own process group, which a timeout or the cancellation signal ends. A non-zero exit
 * is a result the model sees, not a failure.
 *
 * @param command - The command text.
 * @param workingDirectory - The directory to run it in.
 * @param timeoutSeconds - Seconds before the command is killed.
 * @param signal - Aborts the command.
 * @param toolProcessGroups - Registers the command's process group until the shell is reaped, if given.
 * @returns The exit status, then stdout and stderr, each cut at 64 KiB.
 * @throws {ToolExecutionError} When the shell cannot start.
 */
export function runShellCommand(
  command: string,
  workingDirectory: string,
  timeoutSeconds: number,
  signal: AbortSignal,
  toolProcessGroups?: ToolProcessGroupRegistry,
): Promise<string> {
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn(command, {
      cwd: workingDirectory,
      shell: true,
      detached: process.platform !== "win32",
      stdio: ["ignore", "pipe", "pipe"],
    });
    const registration =
      child.pid === undefined || process.platform === "win32" ? undefined : toolProcessGroups?.register(child.pid);
    // "exit" comes after Node reaped the shell; from then on its pid may name another process.
    child.once("exit", () => registration?.release());
    const stdout = new CappedOutput();
    const stderr = new CappedOutput();
    let endReason: string | undefined;
    const killGroup = (reason: string): void => {
      endReason ??= reason;
      if (child.pid !== undefined) {
        try {
          process.kill(process.platform === "win32" ? child.pid : -child.pid, "SIGKILL");
        } catch {
          // The group already exited.
        }
      }
    };
    const timer = setTimeout(() => killGroup(`timed out after ${timeoutSeconds} seconds`), timeoutSeconds * 1000);
    const onAbort = (): void => killGroup("cancelled");
    signal.addEventListener("abort", onAbort, { once: true });
    child.stdout.on("data", (chunk: Buffer) => stdout.append(chunk));
    child.stderr.on("data", (chunk: Buffer) => stderr.append(chunk));
    child.once("error", (error) => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      rejectPromise(new ToolExecutionError(`the shell could not start: ${error.message}`, { cause: error }));
    });
    child.once("close", (code, exitSignal) => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      const status =
        endReason !== undefined
          ? `killed: ${endReason}`
          : code !== null
            ? `exit code ${code}`
            : `ended by signal ${exitSignal ?? "unknown"}`;
      resolvePromise(`${status}\n--- stdout ---\n${stdout.text()}\n--- stderr ---\n${stderr.text()}`);
    });
  });
}

/** Collects at most 64 KiB of a stream. */
class CappedOutput {
  private readonly chunks: Buffer[] = [];
  private byteCount = 0;
  private isCut = false;

  public append(chunk: Buffer): void {
    const room = maximumOutputBytes - this.byteCount;
    if (room <= 0) {
      this.isCut = true;
      return;
    }
    const kept = chunk.length > room ? chunk.subarray(0, room) : chunk;
    this.isCut ||= kept.length < chunk.length;
    this.chunks.push(kept);
    this.byteCount += kept.length;
  }

  public text(): string {
    const text = Buffer.concat(this.chunks).toString("utf8");
    return this.isCut ? `${text}\n[cut at ${maximumOutputBytes} bytes]` : text;
  }
}
