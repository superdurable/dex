// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { constants as osConstants } from "node:os";

import { getShellConfig, type BashOperations } from "@earendil-works/pi-coding-agent";
import { ToolExecutionError } from "@superdurable/dex-agents";

/** The longest a command may run, in seconds: the CLI core shell tool's maximum. */
export const maximumShellTimeoutSeconds = 600;

type ShellExecutionOptions = Parameters<BashOperations["exec"]>[2];

/**
 * Runs one `bash` command for pi-coding-agent's `bash` tool, as its local shell backend does, and records whether
 * the shell process started.
 *
 * Failures before the process starts, such as an invalid timeout, a missing working directory, or a shell that cannot
 * be resolved or spawned, throw {@link ToolExecutionError}: the command did not run. The command runs in its own
 * process group, which a timeout or cancellation kills. A timeout rejects with `timeout:<seconds>` and a
 * cancellation with `aborted`, the errors Pi's `bash` tool reads.
 */
export class LocalShellExecution {
  /** Whether the shell process started. */
  public hasStarted = false;
  /** Whether the timeout killed the command. */
  public hasTimedOut = false;

  public async execute(command: string, workingDirectory: string, options: ShellExecutionOptions): Promise<{ exitCode: number | null }> {
    const { timeout, signal } = options;
    if (timeout !== undefined && (!Number.isFinite(timeout) || timeout <= 0 || timeout > maximumShellTimeoutSeconds)) {
      throw new ToolExecutionError(
        `timeout must be a number of seconds above 0 and at most ${maximumShellTimeoutSeconds}; the command did not run`,
      );
    }
    if (signal?.aborted === true) {
      throw new Error("aborted");
    }
    let shellConfig: ReturnType<typeof getShellConfig>;
    try {
      shellConfig = getShellConfig();
    } catch (error: unknown) {
      throw new ToolExecutionError(`no shell to run the command: ${describe(error)}; the command did not run`, { cause: error });
    }
    const isCommandOnStdin = shellConfig.commandTransport === "stdin";
    const child = spawn(shellConfig.shell, isCommandOnStdin ? shellConfig.args : [...shellConfig.args, command], {
      cwd: workingDirectory,
      detached: process.platform !== "win32",
      ...(options.env === undefined ? {} : { env: options.env }),
      stdio: [isCommandOnStdin ? "pipe" : "ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    return this.waitForCommand(child, isCommandOnStdin ? command : undefined, timeout, options);
  }

  private waitForCommand(
    child: ChildProcess,
    standardInput: string | undefined,
    timeout: number | undefined,
    options: ShellExecutionOptions,
  ): Promise<{ exitCode: number | null }> {
    return new Promise((resolvePromise, rejectPromise) => {
      const killGroup = (): void => {
        if (child.pid === undefined) {
          return;
        }
        try {
          if (process.platform === "win32") {
            child.kill("SIGKILL");
          } else {
            process.kill(-child.pid, "SIGKILL");
          }
        } catch {
          // The process group already exited.
        }
      };
      const timer =
        timeout === undefined
          ? undefined
          : setTimeout(() => {
              this.hasTimedOut = true;
              killGroup();
            }, timeout * 1000);
      const onAbort = (): void => killGroup();
      options.signal?.addEventListener("abort", onAbort, { once: true });
      const cleanUp = (): void => {
        if (timer !== undefined) {
          clearTimeout(timer);
        }
        options.signal?.removeEventListener("abort", onAbort);
      };
      child.once("spawn", () => {
        this.hasStarted = true;
        if (standardInput !== undefined) {
          child.stdin?.on("error", () => undefined);
          child.stdin?.end(standardInput);
        }
      });
      child.stdout?.on("data", (data: Buffer) => options.onData(data));
      child.stderr?.on("data", (data: Buffer) => options.onData(data));
      child.once("error", (error) => {
        cleanUp();
        rejectPromise(
          this.hasStarted
            ? error
            : new ToolExecutionError(`the shell could not start: ${error.message}; the command did not run`, { cause: error }),
        );
      });
      child.once("close", (code, exitSignal) => {
        cleanUp();
        if (options.signal?.aborted === true) {
          rejectPromise(new Error("aborted"));
        } else if (this.hasTimedOut) {
          rejectPromise(new Error(`timeout:${timeout}`));
        } else {
          const signalNumber = exitSignal === null ? undefined : osConstants.signals[exitSignal];
          resolvePromise({ exitCode: code ?? (signalNumber === undefined ? 1 : 128 + signalNumber) });
        }
      });
    });
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
