// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import type { Readable, Writable } from "node:stream";
import { getSystemErrorMessage } from "node:util";

import { ToolExecutionError } from "@superdurable/dex-agents";

import type { ToolProcessGroupRegistry } from "../worker-host/tool-process-groups.js";

/** Seconds a shell command may run when the call sets no timeout. */
export const defaultShellTimeoutSeconds = 120;
/** The longest timeout a call may set, in seconds. */
export const maximumShellTimeoutSeconds = 600;
const maximumOutputBytes = 64 * 1024;
/** How long processes a finished command left in its group get between SIGTERM and SIGKILL. */
const leftBehindGraceMs = 500;
/** How long output may still arrive after the shell, or on POSIX its gatekeeper, exited. */
const outputDrainMs = 1_000;

/**
 * The process group leader of every command: it runs the command with `/bin/sh -c`, closes its own output, reports
 * the exit status on fd 3, and then waits on fd 4, so it stays unreaped until the CLI core has cleaned the group.
 * If the CLI core goes away, fd 4 ends and the leader kills its own group. Its own diagnostics, such as the line a
 * shell prints when a signal ended its child, go to `/dev/null`; the command's stderr reaches it through fd 5.
 */
const gatekeeperScript = [
  "exec 5>&2 2>/dev/null",
  '/bin/sh -c "$1" </dev/null 2>&5 3>&- 4<&- 5>&-',
  "status=$?",
  "exec 1>&- 5>&-",
  "trap '' TERM",
  'printf "%s\\n" "$status" >&3',
  "read -r _ <&4",
  "kill -KILL 0",
].join("\n");

/**
 * Runs one command with the system shell in a directory and returns its exit status and output.
 *
 * The command runs in its own process group. When the command ends, so do the processes it left in that group, such
 * as one it started with `&`: they get SIGTERM, then SIGKILL 0.5 s later, and the call answers once they are gone.
 * A timeout or the cancellation signal kills the whole group at once. A process that left the group, such as one
 * that called `setsid`, is not stopped; the call stops waiting for its output 1 s after the group ended. The status
 * is the number the shell reports, so a command that a signal ended shows 128 plus the signal's number. A non-zero
 * exit is a result the model sees, not a failure.
 *
 * The group's leader is a small gatekeeper shell that runs the command and stays alive until the group is cleaned,
 * so the group's ID cannot name another process while anything signals it: the CLI core signals the group only while
 * the gatekeeper has not reported its exit.
 *
 * @param command - The command text.
 * @param workingDirectory - The directory to run it in.
 * @param timeoutSeconds - Seconds before the command is killed.
 * @param signal - Aborts the command.
 * @param toolProcessGroups - Registers the command's process group until its leader exits, if given.
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
  if (process.platform === "win32") {
    return runWindowsShellCommand(command, workingDirectory, timeoutSeconds, signal);
  }
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn("/bin/sh", ["-c", gatekeeperScript, "dex-shell-command", command], {
      cwd: workingDirectory,
      detached: true,
      stdio: ["ignore", "pipe", "pipe", "pipe", "pipe"],
    });
    const [, childStdout, childStderr] = child.stdio as unknown as [null, Readable, Readable];
    const registration = child.pid === undefined ? undefined : toolProcessGroups?.register(child);
    let drainTimer: NodeJS.Timeout | undefined;
    child.once("exit", () => {
      registration?.release();
      // A process that left the group, such as one that called setsid, may still hold the pipes: stop waiting.
      drainTimer = setTimeout(() => {
        for (const stream of child.stdio) {
          stream?.destroy();
        }
      }, outputDrainMs);
    });
    const stdout = new CappedOutput();
    const stderr = new CappedOutput();
    const output = new OutputEnd([childStdout, childStderr]);
    let endReason: string | undefined;
    let commandStatus: number | undefined;
    const signalGroup = (name: NodeJS.Signals): void => {
      // Node reaps the leader just before it sets these fields, so until then the group ID is still this group's.
      if (child.pid === undefined || child.exitCode !== null || child.signalCode !== null) {
        return;
      }
      try {
        process.kill(-child.pid, name);
      } catch {
        // The group already exited.
      }
    };
    const stopWaiting = (): void => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
    };
    const kill = (reason: string): void => {
      endReason ??= reason;
      stopWaiting();
      signalGroup("SIGKILL");
    };
    const timer = setTimeout(() => kill(`timed out after ${timeoutSeconds} seconds`), timeoutSeconds * 1000);
    const onAbort = (): void => kill("cancelled");
    signal.addEventListener("abort", onAbort, { once: true });
    const statusStream = child.stdio[3] as Readable;
    let statusText = "";
    statusStream.setEncoding("utf8");
    statusStream.on("data", (chunk: string) => {
      statusText += chunk;
      const line = /^(\d+)\n/.exec(statusText);
      if (line === null || commandStatus !== undefined || endReason !== undefined) {
        return;
      }
      commandStatus = Number(line[1]);
      stopWaiting();
      // The command ended: what it left in its group ends too.
      signalGroup("SIGTERM");
      void output.within(leftBehindGraceMs).then(() => signalGroup("SIGKILL"));
    });
    childStdout.on("data", (chunk: Buffer) => stdout.append(chunk));
    childStderr.on("data", (chunk: Buffer) => stderr.append(chunk));
    child.once("error", (error) => {
      stopWaiting();
      rejectPromise(new ToolExecutionError(`the shell could not start: ${describeSpawnError(error)}`, { cause: error }));
    });
    child.once("close", (code, exitSignal) => {
      stopWaiting();
      clearTimeout(drainTimer);
      (child.stdio[4] as Writable | null)?.destroy();
      resolvePromise(
        `${describeEnd(endReason, commandStatus, code, exitSignal)}\n--- stdout ---\n${stdout.text()}\n--- stderr ---\n${stderr.text()}`,
      );
    });
  });
}

/** Returns the status line of a finished command: the shell's status as it reported it. */
function describeEnd(
  endReason: string | undefined,
  commandStatus: number | undefined,
  gatekeeperCode: number | null,
  gatekeeperSignal: NodeJS.Signals | null,
): string {
  if (endReason !== undefined) {
    return `killed: ${endReason}`;
  }
  if (commandStatus === undefined) {
    return gatekeeperCode !== null ? `exit code ${gatekeeperCode}` : `ended by signal ${gatekeeperSignal ?? "unknown"}`;
  }
  // The shell cannot tell "exit 143" from a command that SIGTERM ended, so the number is all it reports.
  return `exit code ${commandStatus}`;
}

/** Starts the shell of a Windows command; tests pass a simulated one. */
export type WindowsShellSpawner = (command: string, workingDirectory: string) => ChildProcess;

const spawnWindowsShell: WindowsShellSpawner = (command, workingDirectory) =>
  spawn(command, { cwd: workingDirectory, shell: true, stdio: ["ignore", "pipe", "pipe"] });

/**
 * Runs a command on Windows, which has no process groups: a timeout or the cancellation signal kills the shell while
 * it runs. Output that a background child writes more than 1 s after the shell exited is not collected.
 *
 * @param spawnShell - Starts the shell; the default spawns it with `shell: true`.
 * @returns The exit status, then stdout and stderr, each cut at 64 KiB.
 */
export function runWindowsShellCommand(
  command: string,
  workingDirectory: string,
  timeoutSeconds: number,
  signal: AbortSignal,
  spawnShell: WindowsShellSpawner = spawnWindowsShell,
): Promise<string> {
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawnShell(command, workingDirectory);
    const childStdout = child.stdout as Readable;
    const childStderr = child.stderr as Readable;
    const stdout = new CappedOutput();
    const stderr = new CappedOutput();
    let endReason: string | undefined;
    const kill = (reason: string): void => {
      // Once the shell exited, its pid may name another process, so it is never signaled.
      if (child.pid === undefined || child.exitCode !== null || child.signalCode !== null) {
        return;
      }
      endReason ??= reason;
      try {
        process.kill(child.pid, "SIGKILL");
      } catch {
        // The shell already exited.
      }
    };
    const timer = setTimeout(() => kill(`timed out after ${timeoutSeconds} seconds`), timeoutSeconds * 1000);
    const onAbort = (): void => kill("cancelled");
    signal.addEventListener("abort", onAbort, { once: true });
    let drainTimer: NodeJS.Timeout | undefined;
    child.once("exit", () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      // A background child may hold the pipes open; it keeps running, and its later output is dropped.
      drainTimer = setTimeout(() => {
        childStdout.destroy();
        childStderr.destroy();
      }, outputDrainMs);
    });
    childStdout.on("data", (chunk: Buffer) => stdout.append(chunk));
    childStderr.on("data", (chunk: Buffer) => stderr.append(chunk));
    child.once("error", (error) => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      rejectPromise(new ToolExecutionError(`the shell could not start: ${describeSpawnError(error)}`, { cause: error }));
    });
    child.once("close", (code: number | null, exitSignal: NodeJS.Signals | null) => {
      clearTimeout(timer);
      clearTimeout(drainTimer);
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

/**
 * Returns the system's text for a failed spawn, such as `No such file or directory`, as the Python CLI core reports
 * it, or Node's message when the error has no system error number.
 */
function describeSpawnError(error: Error): string {
  const errno = (error as NodeJS.ErrnoException).errno;
  if (typeof errno !== "number" || typeof getSystemErrorMessage !== "function") {
    return error.message;
  }
  // libuv writes the text in lower case; the C library, which Python reports, capitalizes it.
  const text = getSystemErrorMessage(errno);
  return text.charAt(0).toUpperCase() + text.slice(1);
}

/** Tells when every stream of a set has ended. */
class OutputEnd {
  private readonly ended: Promise<void>;

  public constructor(streams: readonly Readable[]) {
    this.ended = Promise.all(
      streams.map((stream) => new Promise<void>((resolve) => stream.once("close", () => resolve()))),
    ).then(() => undefined);
  }

  /**
   * Waits until every stream ended, or the time ran out.
   * @param timeoutMs - The longest wait.
   */
  public async within(timeoutMs: number): Promise<void> {
    let timer: NodeJS.Timeout | undefined;
    const timeout = new Promise<void>((resolve) => {
      timer = setTimeout(resolve, timeoutMs);
    });
    await Promise.race([this.ended, timeout]);
    clearTimeout(timer);
  }
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
