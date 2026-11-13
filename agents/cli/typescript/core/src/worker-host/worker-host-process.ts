// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess, type SpawnOptions } from "node:child_process";
import { closeSync, mkdirSync, openSync } from "node:fs";
import { dirname } from "node:path";
import { constants } from "node:os";
import { setTimeout as delay } from "node:timers/promises";

import { WorkerHostStoppedError } from "../terminal-ui/local-request-relay.js";
import type { LocalRequestOutcome } from "../terminal-ui/request-outcome-table.js";
import type { SessionTerminalLease } from "@superdurable/dex-agents";

import { signalProcessGroup } from "./process-identity.js";
import {
  workerHostLeaseMethods,
  workerHostSubcommand,
  type LauncherToWorkerHostMessage,
  type WorkerHostReady,
  type WorkerHostStartRequest,
  type WorkerHostStartupFailed,
  type WorkerHostToLauncherMessage,
} from "./worker-host-protocol.js";

/** The session to open does not exist, has another Flow type, or does not run. */
export class SessionUnavailableError extends Error {}

/** How long each step of a Worker host shutdown waits for the host to exit. */
export interface WorkerHostStopBounds {
  /** After asking it to shut down, before SIGTERM. */
  readonly shutdownWaitMs: number;
  /** After SIGTERM to its group, before SIGKILL. */
  readonly terminateGraceMs: number;
  /** After SIGKILL, before giving up on it. */
  readonly killWaitMs: number;
}

const defaultStopBounds: WorkerHostStopBounds = { shutdownWaitMs: 10_000, terminateGraceMs: 2_000, killWaitMs: 2_000 };

/** Starts the Worker host process; tests pass a simulated one. */
export type WorkerHostSpawner = (
  command: string,
  commandArguments: readonly string[],
  options: SpawnOptions,
) => ChildProcess;

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
  private lostLease: { readonly sessionId: string; readonly lease: SessionTerminalLease } | undefined;
  private onLeaseLost: ((sessionId: string, lease: SessionTerminalLease) => void) | undefined;

  /**
   * Starts the Worker host. Its environment is the launcher's, so it holds the provider keys the terminal UI never
   * sees.
   * @param logFilePath - `~/.dex/agents/<harness>.log`.
   * @param environment - The Worker host's environment.
   * @param onStartupLog - Receives log lines before startup ends.
   * @param spawnHost - Starts the process; the default is `child_process.spawn`.
   * @param stopBounds - How long each shutdown step waits.
   */
  public constructor(
    logFilePath: string,
    environment: NodeJS.ProcessEnv,
    private readonly onStartupLog: (text: string) => void,
    spawnHost: WorkerHostSpawner = spawn,
    private readonly stopBounds: WorkerHostStopBounds = defaultStopBounds,
  ) {
    const entryScript = process.argv[1];
    if (entryScript === undefined) {
      throw new Error("the command has no entry script to start its Worker host from");
    }
    mkdirSync(dirname(logFilePath), { recursive: true });
    const logFile = openSync(logFilePath, "a");
    try {
      this.child = spawnHost(process.execPath, [...process.execArgv, entryScript, workerHostSubcommand], {
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
   * Asks the Worker host for a session's lease once, which it then renews.
   * @param sessionId - The session.
   * @returns The RPC's result.
   * @throws {Error} When the attempt failed and may be tried again.
   * @throws {SessionUnavailableError} When the session does not run with this command's Flow type.
   * @throws {WorkerHostStoppedError} When the Worker host is gone, so no later attempt can succeed.
   */
  public async acquireSessionLease(
    sessionId: string,
  ): Promise<{ readonly isAcquired: boolean; readonly lease: SessionTerminalLease | null }> {
    const outcome = await this.execute(workerHostLeaseMethods.acquire, { session_id: sessionId }, "");
    if ("error" in outcome) {
      if (outcome.error.code === "host_stopped") {
        throw new WorkerHostStoppedError(outcome.error.message);
      }
      throw outcome.error.code === "session_unavailable"
        ? new SessionUnavailableError(outcome.error.message)
        : new Error(outcome.error.message);
    }
    const result = outcome.result as { is_acquired?: unknown; lease?: SessionTerminalLease | null };
    return { isAcquired: result.is_acquired === true, lease: result.lease ?? null };
  }

  /**
   * Asks the Worker host to release a session's lease and stop renewing it.
   * @param sessionId - The session.
   */
  public async releaseSessionLease(sessionId: string): Promise<void> {
    await this.execute(workerHostLeaseMethods.release, { session_id: sessionId }, "").catch(() => undefined);
  }

  /**
   * Sets who hears that another terminal took the lease; one that was lost already is delivered at once.
   * @param handler - Receives the session and the other holder's lease.
   */
  public whenSessionLeaseLost(handler: (sessionId: string, lease: SessionTerminalLease) => void): void {
    this.onLeaseLost = handler;
    if (this.lostLease !== undefined) {
      handler(this.lostLease.sessionId, this.lostLease.lease);
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
   * later, SIGKILL, and waits 2 s more.
   * @returns Its exit status, or `undefined` when it still did not exit, such as a process stuck in the kernel.
   */
  public async shutdown(): Promise<number | undefined> {
    if (this.exitCode !== undefined) {
      return this.exitCode;
    }
    this.send({ type: "shutdown" });
    if (await this.waitForExit(this.stopBounds.shutdownWaitMs)) {
      return this.exitCode;
    }
    const pgid = this.child.pid;
    if (pgid !== undefined) {
      signalProcessGroup(pgid, "SIGTERM");
      if (await this.waitForExit(this.stopBounds.terminateGraceMs)) {
        return this.exitCode;
      }
      signalProcessGroup(pgid, "SIGKILL");
    }
    return (await this.waitForExit(this.stopBounds.killWaitMs)) ? this.exitCode : undefined;
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
      case "session_lease_lost":
        this.lostLease = { sessionId: message.sessionId, lease: message.lease };
        this.onLeaseLost?.(message.sessionId, message.lease);
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
