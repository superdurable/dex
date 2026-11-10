// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";
import { closeSync, mkdirSync, mkdtempSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
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
 * Web, 8802) and `DEX_AGENTS_DEX_PORT` (Dex, 8801). It trusts one that a command started, when the record's
 * process is alive on the same ports. Any other server must prove it is a `dexcli dev`: its Dex Web `/readyz`
 * answers with Dex Web's readiness JSON and its Dex port answers the Dex FlowService. Otherwise the command stops
 * instead of sending sessions and connector calls to an unknown server. Without any server, it starts
 * `dexcli dev` on those ports in the background, with its output in `~/.dex/agents/dexcli-dev.log`, waits until
 * it is ready, and records it in `~/.dex/agents/dexcli-dev.json`. The started server keeps running after the
 * command exits, so later commands and Dex Web reuse it.
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
    if (!isStartedByCommand) {
      await requireDexcliDevIdentity(webUrl, serverAddress, ready.body);
    }
    return {
      serverAddress,
      isTls: false,
      webUrl,
      autoConnectorsMcpUrl: (isStartedByCommand ? record?.connectorsMcpUrl : undefined) ?? `${webUrl}/mcp`,
      origin: "reused",
    };
  }
  const connectorsMcpUrl = await startDexcliDev(environment, log);
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

async function startDexcliDev(environment: CliEnvironment, log: (line: string) => void): Promise<string | undefined> {
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
      log(`dexcli dev is running (pid ${record.pid}) and keeps running after this command exits.`);
      return connectorsMcpUrl;
    }
    if (exitDescription !== undefined || Date.now() >= deadline) {
      if (exitDescription === undefined) {
        child.kill("SIGTERM");
      }
      throw new Error(
        `dexcli dev ${exitDescription ?? `was not ready within ${startTimeoutMs / 1000} seconds`}; ` +
          `the end of ${logPath}:\n${output.slice(-2000)}`,
      );
    }
    await delay(pollIntervalMs);
  }
}

/** What `/readyz` answered: nothing, a not-ready status, or ready with its body. */
type Readiness =
  | { readonly state: "absent" }
  | { readonly state: "starting" }
  | { readonly state: "ready"; readonly body: string };

async function probeReadiness(webUrl: string): Promise<Readiness> {
  try {
    const response = await fetch(`${webUrl}/readyz`, { signal: AbortSignal.timeout(readinessProbeTimeoutMs) });
    const body = await response.text();
    return response.ok ? { state: "ready", body } : { state: "starting" };
  } catch {
    return { state: "absent" };
  }
}

async function waitForReadiness(webUrl: string, timeoutMs: number): Promise<{ readonly body: string }> {
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
 * Checks that a server this command did not start is a `dexcli dev`: Dex Web's readiness JSON on the web port and
 * the Dex FlowService on the Dex port.
 */
async function requireDexcliDevIdentity(webUrl: string, serverAddress: string, readinessBody: string): Promise<void> {
  const refuse = (reason: string): never => {
    throw new Error(
      `${webUrl} answers, but it is not a dexcli dev (${reason}). Stop that server, or set DEX_AGENTS_WEB_PORT and DEX_AGENTS_DEX_PORT to the ports of a dexcli dev or to free ports.`,
    );
  };
  let readiness: unknown;
  try {
    readiness = JSON.parse(readinessBody);
  } catch {
    return refuse("its /readyz is not Dex Web's readiness JSON");
  }
  const fields = (readiness ?? {}) as Record<string, unknown>;
  if (
    fields.status !== "ready" ||
    typeof fields.definitionRevision !== "string" ||
    typeof fields.source !== "string" ||
    typeof fields.definitionCount !== "number"
  ) {
    return refuse("its /readyz is not Dex Web's readiness JSON");
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
    const timeout = delay(identityProbeTimeoutMs).then(() => "it did not answer within 5 seconds");
    return await Promise.race([answer, timeout]);
  } finally {
    await client.close().catch(() => undefined);
    blobCache.close();
    rmSync(cacheDirectory, { recursive: true, force: true });
  }
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
