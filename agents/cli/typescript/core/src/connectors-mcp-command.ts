// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { closeSync, mkdirSync, openSync, readFileSync } from "node:fs";
import { constants } from "node:os";
import { join } from "node:path";

import type { CliEnvironment } from "./cli-environment.js";

/** The first `dexcli` release with `dexcli connectors mcp`. */
export const connectorsMcpCommandMinimumDexcliVersion = "1.47.7";

// The first start may download the Connectors MCP server and retry five failed starts.
const readyTimeoutMs = 120_000;
// A child that closed its output without a line exits at once when it hit a usage error.
const exitWaitAfterOutputEndMs = 5_000;
const stdinCloseWaitMs = 10_000;
const groupStopWaitMs = 5_000;
// The same bound as dexcli's connectorsMCPMaxAnnouncementBytes.
const maximumReadyLineBytes = 1 << 20;
const maximumReasonCharacters = 500;
// dexcli answers an unknown command with this usage exit code.
const usageExitCode = 2;

/** How `dexcli connectors mcp` ended its startup. */
export type ConnectorsMcpCommandStart =
  | { readonly kind: "ready"; readonly url: string }
  | { readonly kind: "unsupported" }
  | { readonly kind: "failed"; readonly reason: string };

/** Whether this machine's `connections.json` holds any connection. */
export type LocalConnectionsState = "missing" | "empty" | "present";

/**
 * Reads `~/.dex/connectors/connections.json`, the file the Dex Web Connectors page of a local `dexcli dev` writes.
 * @param environment - The command's environment settings.
 * @returns `missing` without the file, `empty` when it lists no connection, else `present`, also for a file the
 * Connectors MCP server must judge.
 */
export function readLocalConnectionsState(environment: CliEnvironment): LocalConnectionsState {
  let content: string;
  try {
    content = readFileSync(join(environment.homeDirectory, ".dex", "connectors", "connections.json"), "utf8");
  } catch {
    return "missing";
  }
  try {
    const parsed = JSON.parse(content) as { connections?: unknown } | null;
    return parsed !== null && Array.isArray(parsed.connections) && parsed.connections.length === 0 ? "empty" : "present";
  } catch {
    return "present";
  }
}

/** `~/.dex/agents/dexcli-connectors-mcp.log`, the stderr of the last `dexcli connectors mcp` a command ran. */
export function connectorsMcpCommandLogPath(environment: CliEnvironment): string {
  return join(environment.agentsDirectory, "dexcli-connectors-mcp.log");
}

/**
 * Starts `dexcli connectors mcp --exit-on-stdin-close`; the caller owns the returned process from now on.
 *
 * The child leads its own process group, so a Ctrl-C at the terminal does not stop it. It stops when its stdin
 * closes, which happens when this process exits for any reason. Its stderr goes to
 * {@link connectorsMcpCommandLogPath}.
 *
 * @param dexcli - The `dexcli` executable.
 * @param environment - The command's environment settings.
 * @returns The started process; {@link StartedConnectorsMcpCommand.awaitReady} reads its URL.
 * @throws {Error} When the log file cannot be opened.
 */
export function spawnConnectorsMcpCommand(dexcli: string, environment: CliEnvironment): StartedConnectorsMcpCommand {
  mkdirSync(environment.agentsDirectory, { recursive: true });
  const logPath = connectorsMcpCommandLogPath(environment);
  const logFile = openSync(logPath, "w");
  try {
    const child = spawn(dexcli, ["connectors", "mcp", "--exit-on-stdin-close"], {
      detached: true,
      stdio: ["pipe", "pipe", logFile],
      env: environment.childProcessVariables,
    });
    return new StartedConnectorsMcpCommand(child, logPath);
  } finally {
    closeSync(logFile);
  }
}

/** A `dexcli connectors mcp` this command started; stopping it stops a server it started, never a reused one. */
export class StartedConnectorsMcpCommand {
  private readonly exited: Promise<void>;
  private stopping: Promise<void> | undefined;
  private isKillRevoked = false;

  /**
   * Owns a spawned `dexcli connectors mcp`, the leader of its own process group.
   * @param child - The spawned process, with piped stdin and stdout.
   * @param logPath - Its stderr log, which failure reasons name.
   */
  public constructor(
    private readonly child: ChildProcess,
    private readonly logPath: string,
  ) {
    this.exited = new Promise((resolve) => {
      if (child.exitCode !== null || child.signalCode !== null) {
        resolve();
      } else {
        child.once("exit", () => resolve());
      }
    });
    // A child that exits closes its stdin; the write error must not end the command.
    child.stdin?.on("error", () => undefined);
    child.once("error", () => undefined);
  }

  /**
   * Waits for the one JSON line, at most 120 seconds. Every outcome but `ready` stops the process first.
   * @returns `ready` with the proxy URL; `unsupported` for a `dexcli` that may lack the command; `failed` otherwise.
   */
  public async awaitReady(): Promise<ConnectorsMcpCommandStart> {
    let start: ConnectorsMcpCommandStart;
    try {
      start = this.interpret(await readFirstLine(this.child, readyTimeoutMs));
    } catch (error: unknown) {
      start = { kind: "failed", reason: boundedReason(error instanceof Error ? error.message : String(error)) };
    }
    if (start.kind === "ready") {
      this.child.stdout?.destroy();
      // Its stdin stays open until the command exits, but must not keep the event loop alive.
      (this.child.stdin as { unref?: () => void } | null)?.unref?.();
      this.child.unref();
      return start;
    }
    // Unread output must not block the child while it stops.
    this.child.stdout?.destroy();
    await this.stop();
    return start.kind === "failed" ? { kind: "failed", reason: `${start.reason}; see ${this.logPath}` } : start;
  }

  /**
   * Stops it once: closes its stdin, then signals its process group with SIGTERM and then SIGKILL while the leader
   * runs. A real dexcli stops its own server on stdin EOF, so nothing of the group outlives the leader.
   * @returns A Promise that resolves after the leader exited or the last wait ended.
   */
  public stop(): Promise<void> {
    this.stopping ??= this.doStop().finally(() => {
      this.isKillRevoked = true;
    });
    return this.stopping;
  }

  /** Kills its process group at once while the leader runs, as a second signal does; a no-op after {@link stop}. */
  public kill(): void {
    if (!this.isKillRevoked) {
      this.signalGroupWhileLeaderRuns("SIGKILL");
    }
  }

  private interpret(outcome: FirstLineOutcome): ConnectorsMcpCommandStart {
    switch (outcome.kind) {
      case "failed":
        return { kind: "failed", reason: outcome.reason };
      case "exited":
        return outcome.exitCode === usageExitCode
          ? { kind: "unsupported" }
          : { kind: "failed", reason: `dexcli connectors mcp exited with ${outcome.exitCode ?? outcome.signal}` };
      case "line":
        return interpretReadyLine(outcome.line);
    }
  }

  private async doStop(): Promise<void> {
    this.child.stdin?.end();
    if (await resolvesWithin(this.exited, stdinCloseWaitMs)) {
      return;
    }
    this.signalGroupWhileLeaderRuns("SIGTERM");
    if (await resolvesWithin(this.exited, groupStopWaitMs)) {
      return;
    }
    this.signalGroupWhileLeaderRuns("SIGKILL");
    await resolvesWithin(this.exited, groupStopWaitMs);
  }

  /** Node reaps the leader just before it sets these fields, so until then its group ID stays this group's. */
  private isLeaderRunning(): boolean {
    return this.child.exitCode === null && this.child.signalCode === null;
  }

  private signalGroupWhileLeaderRuns(signal: NodeJS.Signals): void {
    const pid = this.child.pid;
    if (pid === undefined || !this.isLeaderRunning()) {
      return;
    }
    try {
      if (process.platform === "win32") {
        this.child.kill(signal);
      } else {
        process.kill(-pid, signal);
      }
    } catch {
      // The group is already gone.
    }
  }
}

/**
 * While a command owns a `dexcli connectors mcp` and no other handler does, SIGTERM, SIGHUP, and SIGINT stop it
 * and exit with 128 plus the signal number; a second signal kills it at once. The chat and the terminal UI take
 * the signals over with {@link releaseInterrupt} and {@link release}, as they do from a started `dexcli dev`.
 */
export class ConnectorsMcpSignalCleanup {
  private isStopping = false;
  private readonly listeners = new Map<NodeJS.Signals, () => void>();

  /**
   * Starts listening at once.
   * @param started - The process the command owns.
   */
  public constructor(private readonly started: StartedConnectorsMcpCommand) {
    this.listen();
  }

  /** Leaves Ctrl-C to the chat, which cancels a turn on the first one. */
  public releaseInterrupt(): void {
    this.remove("SIGINT");
  }

  /** Leaves every signal to another handler, such as the plain chat's or the terminal UI handoff's. */
  public release(): void {
    for (const signal of [...this.listeners.keys()]) {
      this.remove(signal);
    }
  }

  /** Takes the signals back after the terminal UI handoff, unless a stop already began. */
  public reclaim(): void {
    if (!this.isStopping) {
      this.listen();
    }
  }

  private listen(): void {
    for (const signal of ["SIGTERM", "SIGHUP", "SIGINT"] as const) {
      if (!this.listeners.has(signal)) {
        const listener = (): void => this.onSignal(signal);
        this.listeners.set(signal, listener);
        process.on(signal, listener);
      }
    }
  }

  private onSignal(signal: NodeJS.Signals): void {
    const exitCode = 128 + constants.signals[signal];
    if (this.isStopping) {
      this.started.kill();
      process.exit(exitCode);
    }
    this.isStopping = true;
    void this.started.stop().then(() => process.exit(exitCode));
  }

  private remove(signal: NodeJS.Signals): void {
    const listener = this.listeners.get(signal);
    if (listener !== undefined) {
      process.off(signal, listener);
      this.listeners.delete(signal);
    }
  }
}

/** Reads a ready line: a JSON object with type ready and a loopback http url, or the reason it is not one. */
function interpretReadyLine(line: string): ConnectorsMcpCommandStart {
  if (line === "") {
    return { kind: "failed", reason: "dexcli connectors mcp printed an empty line" };
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(line);
  } catch {
    parsed = undefined;
  }
  if (typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)) {
    const fields = parsed as { type?: unknown; url?: unknown; message?: unknown };
    if (fields.type === "ready" && typeof fields.url === "string" && isLoopbackHttpUrl(fields.url)) {
      return { kind: "ready", url: fields.url };
    }
    if (fields.type === "error" && typeof fields.message === "string") {
      return { kind: "failed", reason: boundedReason(fields.message) };
    }
  }
  return { kind: "failed", reason: `unexpected output: ${boundedReason(line)}` };
}

type FirstLineOutcome =
  | { readonly kind: "line"; readonly line: string }
  | { readonly kind: "exited"; readonly exitCode: number | null; readonly signal: NodeJS.Signals | null }
  | { readonly kind: "failed"; readonly reason: string };

/**
 * Resolves with the first stdout line, at most {@link maximumReadyLineBytes}. A line printed just before exit still
 * wins, since it ends stdout first. Output that ends without a line waits a little for the exit code.
 */
function readFirstLine(child: ChildProcess, timeoutMs: number): Promise<FirstLineOutcome> {
  return new Promise((resolve) => {
    const chunks: Buffer[] = [];
    let bufferedBytes = 0;
    let exitWaitTimer: NodeJS.Timeout | undefined;
    let exit: { exitCode: number | null; signal: NodeJS.Signals | null } | undefined;
    let isOutputEnded = child.stdout === null;
    const finish = (outcome: FirstLineOutcome): void => {
      clearTimeout(timer);
      clearTimeout(exitWaitTimer);
      child.stdout?.off("data", onData);
      child.stdout?.off("end", onOutputEnd);
      child.off("exit", onExit);
      child.off("error", onError);
      resolve(outcome);
    };
    const finishAfterOutputEnds = (): void => {
      if (!isOutputEnded) {
        return;
      }
      const line = Buffer.concat(chunks).toString("utf8").trim();
      if (line !== "") {
        finish({ kind: "line", line });
      } else if (exit !== undefined) {
        finish({ kind: "exited", ...exit });
      } else {
        exitWaitTimer ??= setTimeout(() => {
          finish({ kind: "failed", reason: "dexcli connectors mcp closed its output without a line" });
        }, exitWaitAfterOutputEndMs);
      }
    };
    const onData = (chunk: Buffer): void => {
      const newline = chunk.indexOf(0x0a);
      const kept = newline >= 0 ? chunk.subarray(0, newline) : chunk;
      chunks.push(kept);
      bufferedBytes += kept.length;
      if (bufferedBytes > maximumReadyLineBytes) {
        finish({ kind: "failed", reason: `dexcli connectors mcp printed more than ${maximumReadyLineBytes} bytes without a newline` });
      } else if (newline >= 0) {
        finish({ kind: "line", line: Buffer.concat(chunks).toString("utf8").trim() });
      }
    };
    const onOutputEnd = (): void => {
      isOutputEnded = true;
      finishAfterOutputEnds();
    };
    const onExit = (exitCode: number | null, signal: NodeJS.Signals | null): void => {
      exit = { exitCode, signal };
      finishAfterOutputEnds();
    };
    const onError = (error: Error): void => {
      finish({ kind: "failed", reason: `dexcli connectors mcp could not start: ${error.message}` });
    };
    const timer = setTimeout(() => {
      finish({ kind: "failed", reason: `dexcli connectors mcp printed no line within ${timeoutMs / 1000} seconds` });
    }, timeoutMs);
    child.stdout?.on("data", onData);
    child.stdout?.once("end", onOutputEnd);
    child.once("exit", onExit);
    child.once("error", onError);
  });
}

function boundedReason(text: string): string {
  return text.length <= maximumReasonCharacters ? text : `${text.slice(0, maximumReasonCharacters)}...`;
}

function isLoopbackHttpUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === "http:" && (url.hostname === "127.0.0.1" || url.hostname === "localhost" || url.hostname === "[::1]");
  } catch {
    return false;
  }
}

async function resolvesWithin(promise: Promise<void>, timeoutMs: number): Promise<boolean> {
  let timer: NodeJS.Timeout | undefined;
  const timeout = new Promise<boolean>((resolve) => {
    timer = setTimeout(() => resolve(false), timeoutMs);
  });
  try {
    return await Promise.race([promise.then(() => true), timeout]);
  } finally {
    clearTimeout(timer);
  }
}
