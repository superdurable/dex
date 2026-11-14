// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, type ChildProcess } from "node:child_process";
import { closeSync, mkdirSync, openSync, readFileSync } from "node:fs";
import { join } from "node:path";

import type { CliEnvironment } from "./cli-environment.js";

/** The first `dexcli` release with `dexcli connectors mcp`. */
export const connectorsMcpCommandMinimumDexcliVersion = "1.47.7";

// The first start may download the Connectors MCP server and retry five failed starts.
const readyTimeoutMs = 120_000;
const stdinCloseWaitMs = 10_000;
const killWaitMs = 5_000;
// dexcli answers an unknown command with this usage exit code.
const usageExitCode = 2;

/** How `dexcli connectors mcp` ended its startup. */
export type ConnectorsMcpCommandStart =
  | { readonly kind: "ready"; readonly url: string; readonly process: StartedConnectorsMcpCommand }
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
    const parsed = JSON.parse(content) as { connections?: unknown };
    return Array.isArray(parsed.connections) && parsed.connections.length === 0 ? "empty" : "present";
  } catch {
    return "present";
  }
}

/**
 * Runs `dexcli connectors mcp` and waits for its one JSON line.
 *
 * The child runs in its own process group, so a Ctrl-C at the terminal does not stop it. It stops when its stdin
 * closes, which happens when this process exits for any reason. Its stderr goes to
 * `~/.dex/agents/dexcli-connectors-mcp.log`.
 *
 * @param dexcli - The `dexcli` executable.
 * @param environment - The command's environment settings.
 * @returns `ready` with the proxy URL and the owned process; `unsupported` for a `dexcli` without the command;
 * `failed` with the reason otherwise.
 */
export async function startConnectorsMcpCommand(
  dexcli: string,
  environment: CliEnvironment,
): Promise<ConnectorsMcpCommandStart> {
  mkdirSync(environment.agentsDirectory, { recursive: true });
  const logPath = connectorsMcpCommandLogPath(environment);
  const logFile = openSync(logPath, "w");
  let child: ChildProcess;
  try {
    child = spawn(dexcli, ["connectors", "mcp", "--exit-on-stdin-close"], {
      detached: true,
      stdio: ["pipe", "pipe", logFile],
      env: environment.childProcessVariables,
    });
  } finally {
    closeSync(logFile);
  }
  const started = new StartedConnectorsMcpCommand(child);
  const outcome = await readFirstLine(child, readyTimeoutMs);
  if (outcome.kind === "line") {
    let parsed: { type?: unknown; url?: unknown; message?: unknown };
    try {
      parsed = JSON.parse(outcome.line) as typeof parsed;
    } catch {
      parsed = {};
    }
    if (parsed.type === "ready" && typeof parsed.url === "string" && isLoopbackHttpUrl(parsed.url)) {
      child.stdout?.destroy();
      // Its stdin stays open until the command exits, but must not keep the event loop alive.
      (child.stdin as { unref?: () => void } | null)?.unref?.();
      child.unref();
      return { kind: "ready", url: parsed.url, process: started };
    }
    await started.stop();
    const reason = typeof parsed.message === "string" ? parsed.message : `unexpected output: ${outcome.line}`;
    return { kind: "failed", reason: `${reason}; see ${logPath}` };
  }
  if (outcome.kind === "exited") {
    if (outcome.exitCode === usageExitCode) {
      return { kind: "unsupported" };
    }
    return { kind: "failed", reason: `dexcli connectors mcp exited with ${outcome.exitCode ?? outcome.signal}; see ${logPath}` };
  }
  await started.stop();
  return { kind: "failed", reason: `${outcome.reason}; see ${logPath}` };
}

/** `~/.dex/agents/dexcli-connectors-mcp.log`, the stderr of the last `dexcli connectors mcp` a command ran. */
export function connectorsMcpCommandLogPath(environment: CliEnvironment): string {
  return join(environment.agentsDirectory, "dexcli-connectors-mcp.log");
}

/** A `dexcli connectors mcp` this command started; stopping it stops a server it started, never a reused one. */
export class StartedConnectorsMcpCommand {
  private readonly exited: Promise<void>;
  private stopping: Promise<void> | undefined;

  /**
   * Owns a spawned `dexcli connectors mcp`.
   * @param child - The spawned process, with piped stdin.
   */
  public constructor(private readonly child: ChildProcess) {
    this.exited = new Promise((resolve) => {
      if (child.exitCode !== null || child.signalCode !== null) {
        resolve();
      } else {
        child.once("exit", () => resolve());
      }
    });
    // A child that exits closes its stdin; the write error must not end the command.
    child.stdin?.on("error", () => undefined);
  }

  /**
   * Stops it once: closes its stdin, then sends SIGTERM, then SIGKILL.
   * @returns A Promise that resolves after it exited or the last wait ended.
   */
  public stop(): Promise<void> {
    this.stopping ??= this.doStop();
    return this.stopping;
  }

  private async doStop(): Promise<void> {
    this.child.stdin?.end();
    if (await resolvesWithin(this.exited, stdinCloseWaitMs)) {
      return;
    }
    signalIgnoringExit(this.child, "SIGTERM");
    if (await resolvesWithin(this.exited, killWaitMs)) {
      return;
    }
    signalIgnoringExit(this.child, "SIGKILL");
    await resolvesWithin(this.exited, killWaitMs);
  }
}

type FirstLineOutcome =
  | { readonly kind: "line"; readonly line: string }
  | { readonly kind: "exited"; readonly exitCode: number | null; readonly signal: NodeJS.Signals | null }
  | { readonly kind: "failed"; readonly reason: string };

/** Resolves with the first stdout line; a line printed just before exit still wins, since it ends stdout first. */
function readFirstLine(child: ChildProcess, timeoutMs: number): Promise<FirstLineOutcome> {
  return new Promise((resolve) => {
    let buffered = "";
    let isStdoutEnded = child.stdout === null;
    let exit: { exitCode: number | null; signal: NodeJS.Signals | null } | undefined;
    const finish = (outcome: FirstLineOutcome): void => {
      clearTimeout(timer);
      child.stdout?.off("data", onData);
      child.stdout?.off("end", onStdoutEnd);
      child.off("exit", onExit);
      child.off("error", onError);
      resolve(outcome);
    };
    const finishAfterExit = (): void => {
      if (exit === undefined || !isStdoutEnded) {
        return;
      }
      const line = buffered.trim();
      finish(line === "" ? { kind: "exited", ...exit } : { kind: "line", line });
    };
    const onData = (chunk: Buffer): void => {
      buffered += chunk.toString("utf8");
      const newline = buffered.indexOf("\n");
      if (newline >= 0) {
        finish({ kind: "line", line: buffered.slice(0, newline).trim() });
      }
    };
    const onStdoutEnd = (): void => {
      isStdoutEnded = true;
      finishAfterExit();
    };
    const onExit = (exitCode: number | null, signal: NodeJS.Signals | null): void => {
      exit = { exitCode, signal };
      finishAfterExit();
    };
    const onError = (error: Error): void => {
      finish({ kind: "failed", reason: `dexcli connectors mcp could not start: ${error.message}` });
    };
    const timer = setTimeout(() => {
      finish({ kind: "failed", reason: `dexcli connectors mcp printed no line within ${timeoutMs / 1000} seconds` });
    }, timeoutMs);
    child.stdout?.on("data", onData);
    child.stdout?.once("end", onStdoutEnd);
    child.once("exit", onExit);
    child.once("error", onError);
  });
}

function isLoopbackHttpUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return url.protocol === "http:" && (url.hostname === "127.0.0.1" || url.hostname === "localhost" || url.hostname === "[::1]");
  } catch {
    return false;
  }
}

function signalIgnoringExit(child: ChildProcess, signal: NodeJS.Signals): void {
  try {
    child.kill(signal);
  } catch {
    // It exited between the wait and the signal.
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
