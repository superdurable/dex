// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";
import { constants } from "node:os";
import type { Readable, Writable } from "node:stream";

import { ToolExecutionError } from "@superdurable/dex-agents";

import type { ToolProcessGroupRegistry } from "../worker-host/tool-process-groups.js";

/** Seconds a shell command may run when the call sets no timeout. */
export const defaultShellTimeoutSeconds = 120;
/** The longest timeout a call may set, in seconds. */
export const maximumShellTimeoutSeconds = 600;
const maximumOutputBytes = 64 * 1024;
/** How long processes a finished command left in its group get between SIGTERM and SIGKILL. */
const leftBehindGraceMs = 500;

/**
 * The process group leader of every command: it runs the command with `/bin/sh -c`, closes its own output, reports
 * the exit status on fd 3, and then waits on fd 4, so it stays unreaped until the CLI core has cleaned the group.
 * If the CLI core goes away, fd 4 ends and the leader kills its own group.
 */
const gatekeeperScript = [
  '/bin/sh -c "$1" </dev/null 3>&- 4<&-',
  "status=$?",
  "exec 1>&- 2>&-",
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
 * A timeout or the cancellation signal kills the whole group at once. A non-zero exit is a result the model sees,
 * not a failure.
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
    child.once("exit", () => registration?.release());
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
      rejectPromise(new ToolExecutionError(`the shell could not start: ${error.message}`, { cause: error }));
    });
    child.once("close", (code, exitSignal) => {
      stopWaiting();
      (child.stdio[4] as Writable | null)?.destroy();
      resolvePromise(
        `${describeEnd(endReason, commandStatus, code, exitSignal)}\n--- stdout ---\n${stdout.text()}\n--- stderr ---\n${stderr.text()}`,
      );
    });
  });
}

/** Returns the status line of a finished command. */
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
  // The shell reports a command that a signal ended as 128 plus the signal's number.
  if (commandStatus > 128) {
    const name = Object.entries(constants.signals).find(([, number]) => number === commandStatus - 128)?.[0];
    return `ended by signal ${name ?? String(commandStatus - 128)}`;
  }
  return `exit code ${commandStatus}`;
}

/** Runs a command on Windows, which has no process groups to clean: a timeout or cancellation kills the shell. */
function runWindowsShellCommand(
  command: string,
  workingDirectory: string,
  timeoutSeconds: number,
  signal: AbortSignal,
): Promise<string> {
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn(command, { cwd: workingDirectory, shell: true, stdio: ["ignore", "pipe", "pipe"] });
    const stdout = new CappedOutput();
    const stderr = new CappedOutput();
    let endReason: string | undefined;
    const kill = (reason: string): void => {
      if (child.exitCode === null && child.signalCode === null) {
        endReason ??= reason;
        child.kill("SIGKILL");
      }
    };
    const timer = setTimeout(() => kill(`timed out after ${timeoutSeconds} seconds`), timeoutSeconds * 1000);
    const onAbort = (): void => kill("cancelled");
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
