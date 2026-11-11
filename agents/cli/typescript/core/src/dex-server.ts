// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import {
  closeSync,
  existsSync,
  linkSync,
  mkdirSync,
  mkdtempSync,
  openSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { constants, tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import { Client, FlowNotFoundError, Registry, openBlobCache } from "@superdurable/dex";

import type { CliEnvironment } from "./cli-environment.js";
import { resolveDexcliExecutable } from "./dexcli-binary.js";

const readyLine = "Dex development environment is ready";
const connectorsMcpLine = /Connectors MCP:\s*(\S+)/;
const readinessProbeTimeoutMs = 2_000;
const startTimeoutMs = 120_000;
const reuseWaitTimeoutMs = 60_000;
const pollIntervalMs = 250;
const identityProbeTimeoutMs = 5_000;
const identityProbeFlowId = "dex-agents-cli-identity-probe";
// Longer than the 10 seconds dexcli dev gives dex-server-zd to shut down.
const stopGracePeriodMs = 20_000;
const killWaitMs = 5_000;
// dexcli dev stops dex-server-zd before it exits; this only catches a member that outlives it.
const leftoverWaitMs = 2_000;
const stoppedLine = "Stopped the dexcli dev this command started.";
const sharingHint = "To share one dexcli dev across sessions, run dexcli dev yourself first.";

/**
 * The Dex server a command uses.
 *
 * @since 0.5.0
 */
export interface DexServerConnection {
  /** The Dex gRPC address for the Client and Worker, `host:port`. */
  readonly serverAddress: string;
  /** Whether the server address needs TLS: an `https` `dex.server`. */
  readonly isTls: boolean;
  /** The Dex Web URL, or `null` when it is unknown because `dex.server` names a server. */
  readonly webUrl: string | null;
  /**
   * The connectors MCP URL that `connectors.mcp: auto` uses: the one `dexcli dev` printed, else `<webUrl>/mcp`;
   * `null` when the Dex Web URL is unknown.
   */
  readonly autoConnectorsMcpUrl: string | null;
  /** What happened: an explicit server, a reused `dexcli dev`, or one this command started. */
  readonly origin: "configured" | "reused" | "started";
}

/**
 * The record of a `dexcli dev` that a command started, in `~/.dex/agents/dexcli-dev.json`.
 *
 * @since 0.5.0
 */
export interface StartedDexcliDev {
  /** Process ID of `dexcli dev`. */
  readonly pid: number;
  /** Its Dex gRPC port. */
  readonly dexPort: number;
  /** Its Dex Web port. */
  readonly webPort: number;
  /** The log file of its output. */
  readonly logPath: string;
  /** The connectors MCP URL from its `Connectors MCP:` line, when it printed one. */
  readonly connectorsMcpUrl?: string;
}

/**
 * Returns the Dex server of `dex.server`.
 *
 * For a URL, it returns the URL's address. For `auto`, it reuses the `dexcli dev` on `DEX_AGENTS_WEB_PORT` (Dex
 * Web, 8802) and `DEX_AGENTS_DEX_PORT` (Dex, 8801) only after it proves it is a `dexcli dev`: its Dex Web
 * `/readyz` answers with Dex Web's readiness JSON and its Dex port answers the Dex FlowService. Otherwise the
 * command stops instead of sending sessions and connector calls to an unknown server. The record of a server a
 * command started only says that it may still be starting and which connectors MCP URL it printed. Without any
 * server, it starts
 * `dexcli dev` on those ports in the background, with its output in `~/.dex/agents/dexcli-dev.log`, waits until
 * it is ready, and records it in `~/.dex/agents/dexcli-dev.json`. The `dexcli dev` it starts keeps running after
 * this function returns. A command that `runAgentCli` runs stops the `dexcli dev` it started when it exits.
 *
 * @param server - `dex.server`: `auto` or an `http` or `https` URL.
 * @param environment - The command's environment settings.
 * @param log - Writes progress for the person at the terminal.
 * @returns The connection.
 * @throws {Error} When `dexcli` cannot be found or downloaded, or `dexcli dev` does not become ready.
 * @since 0.5.0
 */
export async function resolveDexServer(
  server: string,
  environment: CliEnvironment,
  log: (line: string) => void,
): Promise<DexServerConnection> {
  return resolveDexServerForCommand(server, environment, log, undefined);
}

/** Resolves `dex.server` as {@link resolveDexServer} does; `startedDexcliDev` owns a `dexcli dev` it starts. */
export async function resolveDexServerForCommand(
  server: string,
  environment: CliEnvironment,
  log: (line: string) => void,
  startedDexcliDev: StartedDexcliDevProcess | undefined,
): Promise<DexServerConnection> {
  if (server !== "auto") {
    const url = new URL(server);
    const isTls = url.protocol === "https:";
    const port = url.port === "" ? (isTls ? "443" : "80") : url.port;
    return { serverAddress: `${url.hostname}:${port}`, isTls, webUrl: null, autoConnectorsMcpUrl: null, origin: "configured" };
  }
  const webUrl = `http://127.0.0.1:${environment.dexcliDevWebPort}`;
  const serverAddress = `127.0.0.1:${environment.dexcliDevDexPort}`;
  const record = readStartedDexcliDev(environment);
  const isStartedByCommand =
    record !== undefined &&
    record.dexPort === environment.dexcliDevDexPort &&
    record.webPort === environment.dexcliDevWebPort &&
    isProcessAlive(record.pid);
  const readiness = await probeReadiness(webUrl);
  if (readiness.state !== "absent" || isStartedByCommand) {
    if (readiness.state !== "ready") {
      log(`Waiting for the dexcli dev at ${webUrl} to become ready.`);
    }
    const ready = readiness.state === "ready" ? readiness : await waitForReadiness(webUrl, reuseWaitTimeoutMs);
    await requireDexcliDevIdentity(webUrl, serverAddress, ready);
    return {
      serverAddress,
      isTls: false,
      webUrl,
      autoConnectorsMcpUrl: (isStartedByCommand ? record?.connectorsMcpUrl : undefined) ?? `${webUrl}/mcp`,
      origin: "reused",
    };
  }
  const connectorsMcpUrl = await startDexcliDev(environment, log, startedDexcliDev);
  return {
    serverAddress,
    isTls: false,
    webUrl,
    autoConnectorsMcpUrl: connectorsMcpUrl ?? `${webUrl}/mcp`,
    origin: "started",
  };
}

/**
 * Reads the record of the `dexcli dev` that a command started, if any.
 *
 * @param environment - The command's environment settings.
 * @returns The record, or `undefined`.
 * @since 0.5.0
 */
export function readStartedDexcliDev(environment: CliEnvironment): StartedDexcliDev | undefined {
  try {
    return JSON.parse(readFileSync(startedRecordPath(environment), "utf8")) as StartedDexcliDev;
  } catch {
    return undefined;
  }
}

/** The `dexcli dev` one command spawned; stopping it ends its process group and removes its record. */
export class StartedDexcliDevProcess {
  private child: ChildProcess | undefined;
  private exited: Promise<void> = Promise.resolve();
  private stopping: Promise<void> | undefined;
  private readonly signalListeners = new Map<NodeJS.Signals, () => void>();

  /**
   * Owns no process yet.
   * @param environment - Locates the record.
   * @param log - Writes the stopped line.
   */
  public constructor(
    private readonly environment: CliEnvironment,
    private readonly log: (line: string) => void,
  ) {}

  /**
   * Owns a spawned `dexcli dev`; until it stops, SIGTERM, SIGHUP, and SIGINT stop it and exit.
   * @param child - The spawned group leader.
   */
  public track(child: ChildProcess): void {
    this.child = child;
    this.exited = new Promise((resolve) => {
      if (child.exitCode !== null || child.signalCode !== null) {
        resolve();
      } else {
        child.once("exit", () => resolve());
      }
    });
    this.listenForTerminationSignals();
  }

  /** Leaves Ctrl-C to the terminal chat, which cancels a turn on the first one. */
  public releaseInterrupt(): void {
    this.removeSignalListener("SIGINT");
  }

  /**
   * Stops the group once, SIGTERM then SIGKILL, and removes the record. A signal during the stop kills the group
   * at once and exits.
   * @returns A Promise that resolves after the leader exited, at once when nothing was started.
   */
  public stop(): Promise<void> {
    this.stopping ??= this.doStop();
    return this.stopping;
  }

  private async doStop(): Promise<void> {
    const child = this.child;
    const pid = child?.pid;
    if (child === undefined || pid === undefined) {
      this.removeSignalListeners();
      return;
    }
    this.listenForTerminationSignals();
    removeRecordOf(this.environment, pid);
    const wasRunning = this.isLeaderRunning();
    if (wasRunning) {
      signalProcessGroup(pid, "SIGTERM");
      if (!(await resolvesWithin(this.exited, stopGracePeriodMs))) {
        this.killGroupWhileLeaderRuns();
        await resolvesWithin(this.exited, killWaitMs);
      }
    }
    // Signal 0 delivers nothing, so asking about a group ID that may be reused is harmless.
    const isGroupGone = process.platform === "win32" || (await isTrueWithin(() => !isProcessGroupAlive(pid), leftoverWaitMs));
    if (!isGroupGone) {
      this.writeLine(
        `dexcli dev (pid ${pid}) exited, but processes of its process group may still run; if so, stop them with: kill -- -${pid}`,
      );
    } else if (wasRunning) {
      this.writeLine(stoppedLine);
    }
    this.removeSignalListeners();
  }

  /** Node reaps the leader just before it sets these fields, so until then its group ID stays this group's. */
  private isLeaderRunning(): boolean {
    return this.child !== undefined && this.child.exitCode === null && this.child.signalCode === null;
  }

  private killGroupWhileLeaderRuns(): void {
    const pid = this.child?.pid;
    if (pid !== undefined && this.isLeaderRunning()) {
      signalProcessGroup(pid, "SIGKILL");
    }
  }

  private onTerminationSignal(signal: NodeJS.Signals): void {
    // After SIGHUP the terminal is gone; its read and write errors must not end the command before the stop.
    for (const stream of [process.stdin, process.stdout, process.stderr]) {
      stream.on("error", () => undefined);
    }
    const exitCode = 128 + constants.signals[signal];
    if (this.stopping === undefined) {
      void this.stop().then(() => process.exit(exitCode));
      return;
    }
    // A signal during the grace period means: do not wait any longer.
    this.killGroupWhileLeaderRuns();
    process.exit(exitCode);
  }

  private listenForTerminationSignals(): void {
    for (const signal of ["SIGTERM", "SIGHUP", "SIGINT"] as const) {
      if (!this.signalListeners.has(signal)) {
        const listener = (): void => this.onTerminationSignal(signal);
        this.signalListeners.set(signal, listener);
        process.on(signal, listener);
      }
    }
  }

  private removeSignalListeners(): void {
    for (const signal of [...this.signalListeners.keys()]) {
      this.removeSignalListener(signal);
    }
  }

  private writeLine(line: string): void {
    try {
      this.log(line);
    } catch {
      // After SIGHUP the terminal may be gone; the server is stopped either way.
    }
  }

  private removeSignalListener(signal: NodeJS.Signals): void {
    const listener = this.signalListeners.get(signal);
    if (listener !== undefined) {
      process.off(signal, listener);
      this.signalListeners.delete(signal);
    }
  }
}

/**
 * Describes a reused `dexcli dev` that stopped.
 * @param webUrl - Its Dex Web URL.
 * @returns The short error text, or `undefined` while it still answers.
 */
export async function describeLostDexcliDev(webUrl: string): Promise<string | undefined> {
  if ((await probeReadiness(webUrl)).state !== "absent") {
    return undefined;
  }
  return `the dexcli dev at ${webUrl} stopped. A dexcli dev that another command started stops when that command exits. ${sharingHint}`;
}

async function startDexcliDev(
  environment: CliEnvironment,
  log: (line: string) => void,
  startedDexcliDev: StartedDexcliDevProcess | undefined,
): Promise<string | undefined> {
  const dexcli = await resolveDexcliExecutable(environment, log);
  mkdirSync(environment.agentsDirectory, { recursive: true });
  const logPath = join(environment.agentsDirectory, "dexcli-dev.log");
  writeFileSync(logPath, "");
  const logFile = openSync(logPath, "a");
  const commandArguments = [
    "dev",
    "-open=false",
    "-dex-port",
    String(environment.dexcliDevDexPort),
    "-web-port",
    String(environment.dexcliDevWebPort),
  ];
  log(`Starting dexcli dev (Dex Web ${environment.dexcliDevWebPort}, Dex ${environment.dexcliDevDexPort}); log: ${logPath}`);
  const child = spawn(dexcli, commandArguments, {
    detached: true,
    stdio: ["ignore", logFile, logFile],
    env: environment.childProcessVariables,
  });
  closeSync(logFile);
  startedDexcliDev?.track(child);
  let exitDescription: string | undefined;
  child.once("exit", (code, signal) => {
    exitDescription = `exited with ${code ?? signal ?? "unknown status"}`;
  });
  child.once("error", (error) => {
    exitDescription = `could not start: ${error.message}`;
  });
  const deadline = Date.now() + startTimeoutMs;
  for (;;) {
    const output = readFileSync(logPath, "utf8");
    if (output.includes(readyLine)) {
      child.unref();
      const connectorsMcpUrl = connectorsMcpLine.exec(output)?.[1];
      const record: StartedDexcliDev = {
        pid: child.pid ?? 0,
        dexPort: environment.dexcliDevDexPort,
        webPort: environment.dexcliDevWebPort,
        logPath,
        ...(connectorsMcpUrl === undefined ? {} : { connectorsMcpUrl }),
      };
      writeFileSync(startedRecordPath(environment), `${JSON.stringify(record, null, 2)}\n`);
      log(
        startedDexcliDev === undefined
          ? `dexcli dev is ready (pid ${record.pid}) and keeps running.`
          : `dexcli dev is ready (pid ${record.pid}); it stops when this command exits. ${sharingHint}`,
      );
      return connectorsMcpUrl;
    }
    if (exitDescription !== undefined || Date.now() >= deadline) {
      if (exitDescription === undefined && child.pid !== undefined) {
        signalProcessGroup(child.pid, "SIGTERM");
      }
      throw new Error(
        `dexcli dev ${exitDescription ?? `was not ready within ${startTimeoutMs / 1000} seconds`}; ` +
          `the end of ${logPath}:\n${output.slice(-2000)}`,
      );
    }
    await delay(pollIntervalMs);
  }
}

/** A ready `/readyz` answer: its body, content type, and entity tag. */
interface ReadyAnswer {
  readonly body: string;
  readonly contentType: string;
  readonly entityTag: string | null;
}

/** What `/readyz` answered: nothing, a not-ready status, or ready with its body. */
type Readiness =
  | { readonly state: "absent" }
  | { readonly state: "starting" }
  | { readonly state: "ready"; readonly body: string; readonly contentType: string; readonly entityTag: string | null };

async function probeReadiness(webUrl: string): Promise<Readiness> {
  try {
    const response = await fetch(`${webUrl}/readyz`, { signal: AbortSignal.timeout(readinessProbeTimeoutMs) });
    const body = await response.text();
    return response.ok
      ? {
          state: "ready",
          body,
          contentType: response.headers.get("content-type") ?? "",
          entityTag: response.headers.get("etag"),
        }
      : { state: "starting" };
  } catch {
    return { state: "absent" };
  }
}

async function waitForReadiness(webUrl: string, timeoutMs: number): Promise<ReadyAnswer> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const readiness = await probeReadiness(webUrl);
    if (readiness.state === "ready") {
      return readiness;
    }
    await delay(pollIntervalMs);
  }
  throw new Error(`the dexcli dev at ${webUrl} did not become ready within ${timeoutMs / 1000} seconds`);
}

/**
 * Checks that a server on the ports is a `dexcli dev`: Dex Web's readiness JSON, with its values and entity tag, on
 * the web port and the Dex FlowService on the Dex port. These are protocol checks, not authentication: a local
 * process of another user that binds the ports first and imitates both answers is not detected.
 */
async function requireDexcliDevIdentity(webUrl: string, serverAddress: string, ready: ReadyAnswer): Promise<void> {
  const refuse = (reason: string): never => {
    throw new Error(
      `${webUrl} answers, but it is not a dexcli dev (${reason}). Stop that server, or set DEX_AGENTS_WEB_PORT and DEX_AGENTS_DEX_PORT to the ports of a dexcli dev or to free ports.`,
    );
  };
  let readiness: unknown;
  try {
    readiness = JSON.parse(ready.body);
  } catch {
    return refuse("its /readyz is not Dex Web's readiness JSON");
  }
  const fields = (readiness ?? {}) as Record<string, unknown>;
  const revision = fields.definitionRevision;
  const isReadinessJson =
    ready.contentType.startsWith("application/json") &&
    fields.status === "ready" &&
    typeof revision === "string" &&
    typeof fields.source === "string" &&
    fields.source !== "" &&
    typeof fields.definitionCount === "number" &&
    Number.isSafeInteger(fields.definitionCount) &&
    fields.definitionCount >= 0 &&
    (revision === "" ? ready.entityTag === null : ready.entityTag === JSON.stringify(revision));
  if (!isReadinessJson) {
    return refuse("its /readyz does not answer with Dex Web's readiness JSON and entity tag");
  }
  const reason = await probeDexFlowService(serverAddress);
  if (reason !== undefined) {
    refuse(`${serverAddress} does not answer as a Dex server: ${reason}`);
  }
}

/** Returns why the address does not answer as the Dex FlowService, or `undefined` when it does. */
async function probeDexFlowService(serverAddress: string): Promise<string | undefined> {
  const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-agents-probe-"));
  const blobCache = openBlobCache({ directory: cacheDirectory, maxBytes: 1024 * 1024 });
  const client = new Client(new Registry([]), blobCache, { serverAddress });
  try {
    const answer = client.describeFlow(identityProbeFlowId).then(
      () => undefined,
      (error: unknown) => (error instanceof FlowNotFoundError ? undefined : describeError(error)),
    );
    // Unreferenced, so an answered probe leaves no 5-second timer behind.
    const timeout = delay(identityProbeTimeoutMs, undefined, { ref: false }).then(() => "it did not answer within 5 seconds");
    return await Promise.race([answer, timeout]);
  } finally {
    await client.close().catch(() => undefined);
    blobCache.close();
    rmSync(cacheDirectory, { recursive: true, force: true });
  }
}

/** Signals a group whose leader is still unreaped; Windows has no groups, so taskkill ends the process tree. */
function signalProcessGroup(pid: number, signal: "SIGTERM" | "SIGKILL"): void {
  if (process.platform === "win32") {
    // Node's kill would force-end dexcli dev alone and leave dex-server-zd running.
    const result = spawnSync("taskkill", ["/PID", String(pid), "/T", ...(signal === "SIGKILL" ? ["/F"] : [])], {
      stdio: "ignore",
      windowsHide: true,
    });
    if (result.error !== undefined) {
      // Without taskkill, ending dexcli dev alone is the only stop left.
      killIgnoringExit(pid, signal);
    }
    return;
  }
  killIgnoringExit(-pid, signal);
}

function killIgnoringExit(target: number, signal: NodeJS.Signals): void {
  try {
    process.kill(target, signal);
  } catch {
    // The process already exited.
  }
}

function isProcessGroupAlive(pid: number): boolean {
  try {
    process.kill(-pid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code !== "ESRCH";
  }
}

/** Returns whether a Promise resolves within the timeout. */
async function resolvesWithin(promise: Promise<void>, timeoutMs: number): Promise<boolean> {
  // A referenced timer keeps Node running while it waits for the unreferenced child.
  const timeoutCancellation = new AbortController();
  const timeout = delay(timeoutMs, "timeout" as const, { signal: timeoutCancellation.signal }).catch(() => "cancelled" as const);
  const outcome = await Promise.race([promise.then(() => "resolved" as const), timeout]);
  timeoutCancellation.abort();
  return outcome === "resolved";
}

/** Polls a condition; returns whether it became true within the timeout. */
async function isTrueWithin(condition: () => boolean, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (condition()) {
      return true;
    }
    if (Date.now() >= deadline) {
      return false;
    }
    await delay(pollIntervalMs);
  }
}

/** Removes the record only when it names `pid`; renaming it aside first keeps a record written meanwhile. */
function removeRecordOf(environment: CliEnvironment, pid: number): void {
  const recordPath = startedRecordPath(environment);
  const claimedPath = `${recordPath}.${process.pid}-${randomBytes(4).toString("hex")}.removing`;
  try {
    renameSync(recordPath, claimedPath);
  } catch {
    return; // No record.
  }
  let recordedPid: unknown;
  try {
    recordedPid = (JSON.parse(readFileSync(claimedPath, "utf8")) as { pid?: unknown }).pid;
  } catch {
    recordedPid = undefined;
  }
  if (recordedPid !== pid) {
    try {
      linkSync(claimedPath, recordPath);
    } catch (error: unknown) {
      // EEXIST: a newer record took its place, and that one stays. Without hard links, rename it back.
      if ((error as NodeJS.ErrnoException).code !== "EEXIST" && !existsSync(recordPath)) {
        try {
          renameSync(claimedPath, recordPath);
          return;
        } catch {
          // Losing the other command's record only costs it the connectors URL it recorded.
        }
      }
    }
  }
  rmSync(claimedPath, { force: true });
}

function isProcessAlive(pid: number): boolean {
  if (!Number.isSafeInteger(pid) || pid <= 0) {
    return false;
  }
  try {
    process.kill(pid, 0);
    return true;
  } catch (error: unknown) {
    return (error as NodeJS.ErrnoException).code === "EPERM";
  }
}

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function startedRecordPath(environment: CliEnvironment): string {
  return join(environment.agentsDirectory, "dexcli-dev.json");
}
