// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { spawn } from "node:child_process";
import { closeSync, mkdirSync, openSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

import type { CliEnvironment } from "./cli-environment.js";
import { resolveDexcliExecutable } from "./dexcli-binary.js";

const readyLine = "Dex development environment is ready";
const connectorsMcpLine = /Connectors MCP:\s*(\S+)/;
const readinessProbeTimeoutMs = 2_000;
const startTimeoutMs = 120_000;
const reuseWaitTimeoutMs = 60_000;
const pollIntervalMs = 250;

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
}

/**
 * Returns the Dex server of `dex.server`.
 *
 * For a URL, it returns the URL's address. For `auto`, it reuses the `dexcli dev` whose Dex Web answers
 * `/readyz` on `DEX_AGENTS_WEB_PORT` (8802), with Dex on `DEX_AGENTS_DEX_PORT` (8801). Without one, it starts
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
  const readiness = await probeReadiness(webUrl);
  if (readiness !== "absent") {
    if (readiness === "starting") {
      log(`Waiting for the dexcli dev at ${webUrl} to become ready.`);
      await waitForReadiness(webUrl, reuseWaitTimeoutMs);
    }
    return { serverAddress, isTls: false, webUrl, autoConnectorsMcpUrl: `${webUrl}/mcp`, origin: "reused" };
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
      const record: StartedDexcliDev = {
        pid: child.pid ?? 0,
        dexPort: environment.dexcliDevDexPort,
        webPort: environment.dexcliDevWebPort,
        logPath,
      };
      writeFileSync(startedRecordPath(environment), `${JSON.stringify(record, null, 2)}\n`);
      log(`dexcli dev is running (pid ${record.pid}) and keeps running after this command exits.`);
      return connectorsMcpLine.exec(output)?.[1];
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

async function probeReadiness(webUrl: string): Promise<"ready" | "starting" | "absent"> {
  try {
    const response = await fetch(`${webUrl}/readyz`, { signal: AbortSignal.timeout(readinessProbeTimeoutMs) });
    await response.arrayBuffer();
    return response.ok ? "ready" : "starting";
  } catch {
    return "absent";
  }
}

async function waitForReadiness(webUrl: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if ((await probeReadiness(webUrl)) === "ready") {
      return;
    }
    await delay(pollIntervalMs);
  }
  throw new Error(`the dexcli dev at ${webUrl} did not become ready within ${timeoutMs / 1000} seconds`);
}

function startedRecordPath(environment: CliEnvironment): string {
  return join(environment.agentsDirectory, "dexcli-dev.json");
}
