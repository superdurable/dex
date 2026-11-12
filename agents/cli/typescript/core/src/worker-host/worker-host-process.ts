// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { closeSync, mkdirSync, openSync } from "node:fs";
import { dirname } from "node:path";
import { constants } from "node:os";
import { setTimeout as delay } from "node:timers/promises";

import { WorkerHostStoppedError } from "../terminal-ui/local-request-relay.js";
import type { LocalRequestOutcome } from "../terminal-ui/request-outcome-table.js";
import { signalProcessGroup } from "./process-identity.js";
import {
  workerHostSubcommand,
  type LauncherToWorkerHostMessage,
  type WorkerHostReady,
  type WorkerHostStartRequest,
  type WorkerHostStartupFailed,
  type WorkerHostToLauncherMessage,
} from "./worker-host-protocol.js";

/** How long an orderly Worker host shutdown may take before SIGTERM. */
const shutdownWaitMs = 10_000;
/** How long the Worker host's group gets between SIGTERM and SIGKILL. */
const terminateGraceMs = 2_000;

/** How the Worker host's startup ended. */
export type WorkerHostStartup = WorkerHostReady | WorkerHostStartupFailed | { readonly type: "exited"; readonly exitCode: number };

/** What the launcher hears from its Worker host after startup. */
export interface WorkerHostListener {
  /** A user-facing log line. */
  onLog(text: string): void;
  /** The Worker host began to stop. */
  onStopping(): void;
  /** The Worker host exited, with its exit status or 128 plus the signal number. */
  onExit(exitCode: number): void;
}

/**
 * The launcher's handle on its Worker host: the same command's hidden `__worker-host` entry in its own process
 * group, with stdin on `/dev/null`, stdout and stderr in the command's log file, and a Node IPC control channel.
 */
export class WorkerHostProcess {
  private readonly child: ChildProcess;
  private readonly pending = new Map<number, (outcome: LocalRequestOutcome | WorkerHostStoppedError) => void>();
  private nextRequestNumber = 1;
  private exitCode: number | undefined;
  private readonly exited: Promise<number>;
  private startupResolver: ((startup: WorkerHostStartup) => void) | undefined;
  private listener: WorkerHostListener | undefined;
  private readonly earlyLogs: string[] = [];

  /**
   * Starts the Worker host. Its environment is the launcher's, so it holds the provider keys the terminal UI never
   * sees.
   * @param logFilePath - `~/.dex/agents/<harness>.log`.
   * @param environment - The Worker host's environment.
   * @param onStartupLog - Receives log lines before startup ends.
   */
  public constructor(
    logFilePath: string,
    environment: NodeJS.ProcessEnv,
    private readonly onStartupLog: (text: string) => void,
  ) {
    const entryScript = process.argv[1];
    if (entryScript === undefined) {
      throw new Error("the command has no entry script to start its Worker host from");
    }
    mkdirSync(dirname(logFilePath), { recursive: true });
    const logFile = openSync(logFilePath, "a");
    try {
      this.child = spawn(process.execPath, [...process.execArgv, entryScript, workerHostSubcommand], {
        detached: true,
        stdio: ["ignore", logFile, logFile, "ipc"],
        env: environment,
      });
    } finally {
      closeSync(logFile);
    }
    this.exited = new Promise((resolve) => {
      const end = (exitCode: number): void => {
        if (this.exitCode !== undefined) {
          return;
        }
        this.exitCode = exitCode;
        for (const answer of this.pending.values()) {
          answer(new WorkerHostStoppedError("the Worker host stopped"));
        }
        this.pending.clear();
        this.startupResolver?.({ type: "exited", exitCode });
        this.listener?.onExit(exitCode);
        resolve(exitCode);
      };
      this.child.once("exit", (code, signal) => end(code ?? 128 + (signal === null ? 0 : (constants.signals[signal] ?? 0))));
      // A failed spawn emits "error" and "close" but no "exit".
      this.child.on("error", () => {
        if (this.child.pid === undefined) {
          end(1);
        }
      });
    });
    this.child.on("message", (message: WorkerHostToLauncherMessage) => this.receive(message));
  }

  /** The Worker host's pid, which is also its process group. */
  public get pid(): number | undefined {
    return this.child.pid;
  }

  /** Whether the Worker host exited. */
  public get hasExited(): boolean {
    return this.exitCode !== undefined;
  }

  /**
   * Sends the start request and waits until the session is open, the startup failed, or the Worker host exited.
   * @param start - What the Worker host needs to open the session.
   */
  public start(start: WorkerHostStartRequest): Promise<WorkerHostStartup> {
    return new Promise((resolve) => {
      if (this.exitCode !== undefined) {
        resolve({ type: "exited", exitCode: this.exitCode });
        return;
      }
      this.startupResolver = (startup) => {
        this.startupResolver = undefined;
        resolve(startup);
      };
      this.send(start);
    });
  }

  /**
   * Sets who hears the Worker host after startup. Log lines that arrived in between are delivered first.
   * @param listener - The listener.
   */
  public listen(listener: WorkerHostListener): void {
    this.listener = listener;
    for (const text of this.earlyLogs.splice(0)) {
      listener.onLog(text);
    }
    if (this.exitCode !== undefined) {
      listener.onExit(this.exitCode);
    }
  }

  /**
   * Relays one local request to the Worker host.
   * @returns Its outcome.
   * @throws {WorkerHostStoppedError} When the Worker host is gone or stops before it answers.
   */
  public execute(method: string, params: Readonly<Record<string, unknown>>, idempotencyKey: string): Promise<LocalRequestOutcome> {
    if (this.exitCode !== undefined || !this.child.connected) {
      return Promise.reject(new WorkerHostStoppedError("the Worker host stopped"));
    }
    const requestNumber = this.nextRequestNumber++;
    return new Promise((resolve, reject) => {
      this.pending.set(requestNumber, (outcome) => (outcome instanceof WorkerHostStoppedError ? reject(outcome) : resolve(outcome)));
      this.send({ type: "execute", requestNumber, method, params, idempotencyKey });
    });
  }

  /**
   * Stops the Worker host: asks it to shut down, waits up to 10 s, then sends SIGTERM to its process group and, 2 s
   * later, SIGKILL.
   * @returns Its exit status.
   */
  public async shutdown(): Promise<number> {
    if (this.exitCode !== undefined) {
      return this.exitCode;
    }
    this.send({ type: "shutdown" });
    if (await this.waitForExit(shutdownWaitMs)) {
      return this.exited;
    }
    const pgid = this.child.pid;
    if (pgid !== undefined) {
      signalProcessGroup(pgid, "SIGTERM");
      if (!(await this.waitForExit(terminateGraceMs))) {
        signalProcessGroup(pgid, "SIGKILL");
      }
    }
    return this.exited;
  }

  private async waitForExit(timeoutMs: number): Promise<boolean> {
    const result = await Promise.race([this.exited.then(() => true), delay(timeoutMs, false, { ref: false })]);
    return result;
  }

  private receive(message: WorkerHostToLauncherMessage): void {
    switch (message.type) {
      case "worker_ready":
      case "startup_failed":
        this.startupResolver?.(message);
        return;
      case "log":
        if (this.startupResolver !== undefined) {
          this.onStartupLog(message.text);
        } else if (this.listener === undefined) {
          this.earlyLogs.push(message.text);
        } else {
          this.listener.onLog(message.text);
        }
        return;
      case "executed": {
        const answer = this.pending.get(message.requestNumber);
        this.pending.delete(message.requestNumber);
        answer?.(message.outcome);
        return;
      }
      case "worker_state":
        this.listener?.onStopping();
        return;
    }
  }

  private send(message: LauncherToWorkerHostMessage): void {
    if (this.child.connected) {
      // The callback absorbs a write to a channel that closed meanwhile; "exit" reports the stop.
      this.child.send(message, (error) => error ?? undefined);
    }
  }
}
