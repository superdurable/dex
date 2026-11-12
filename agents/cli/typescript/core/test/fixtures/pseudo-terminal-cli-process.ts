// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import type { Readable } from "node:stream";
import { fileURLToPath } from "node:url";

import { TextCollector, type TestHome } from "./cli-test-support.js";

const cliEntryPath = fileURLToPath(new URL("./scripted-harness-cli.js", import.meta.url));
const defaultWaitMs = 90_000;

// Node cannot open a pseudo-terminal, so Python's pty module relays the bytes and the exit code.
const pseudoTerminalRelayScript = [
  "import os, pty, sys",
  "code = os.waitstatus_to_exitcode(pty.spawn(sys.argv[1:]))",
  "sys.exit(code if code >= 0 else 128 - code)",
].join("\n");

/** The scripted harness command running in a pseudo-terminal, so Ctrl-C and Ctrl-D arrive as terminal bytes. */
export class PseudoTerminalCliProcess {
  /** Everything the command wrote to the terminal. */
  public readonly terminal: TextCollector;
  private readonly exited: Promise<number | null>;

  private constructor(private readonly child: ChildProcess) {
    this.terminal = new TextCollector(child.stdout as Readable);
    this.exited = new Promise((resolve) => child.once("exit", (code) => resolve(code)));
  }

  /**
   * Starts the scripted harness command in a pseudo-terminal.
   * @param home - The test home.
   * @param commandArguments - Arguments after the command name.
   * @returns The process.
   */
  public static start(home: TestHome, commandArguments: readonly string[] = []): PseudoTerminalCliProcess {
    const child = spawn("python3", ["-c", pseudoTerminalRelayScript, process.execPath, cliEntryPath, ...commandArguments], {
      env: { ...home.environment(), TERM: "dumb" },
      stdio: ["pipe", "pipe", "inherit"],
      detached: true,
    });
    return new PseudoTerminalCliProcess(child);
  }

  /**
   * Types text at the terminal.
   * @param text - The bytes, such as `"\x03"` for Ctrl-C.
   */
  public type(text: string): void {
    this.child.stdin?.write(text);
  }

  /**
   * Kills the relay and the command with SIGKILL.
   * @returns A Promise that resolves after the relay exited.
   */
  public async kill(): Promise<void> {
    if (this.child.exitCode === null && this.child.pid !== undefined) {
      try {
        process.kill(-this.child.pid, "SIGKILL");
      } catch {
        // Already gone.
      }
    }
    await this.exited;
  }

  /**
   * Waits for the command to exit.
   * @param timeoutMs - How long to wait.
   * @returns The exit code.
   */
  public async waitForExit(timeoutMs = defaultWaitMs): Promise<number | null> {
    let timer: NodeJS.Timeout | undefined;
    const timeout = new Promise<"timeout">((resolve) => {
      timer = setTimeout(() => resolve("timeout"), timeoutMs);
    });
    try {
      const result = await Promise.race([this.exited, timeout]);
      if (result === "timeout") {
        throw new Error(`the command did not exit; terminal:\n${this.terminal.all}`);
      }
      return result;
    } finally {
      clearTimeout(timer);
    }
  }
}
