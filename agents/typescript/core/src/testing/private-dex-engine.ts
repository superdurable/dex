// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { existsSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { createServer, type Server } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

import { ChildProcessGroup } from "./child-process-group.js";

const engineReadyLine = "Dex development environment is ready";
const startAttempts = 3;

/** Options of {@link PrivateDexEngine.start}. */
export interface PrivateDexEngineOptions {
  /**
   * Path to the `dexcli` executable. Defaults to the `DEXCLI` environment variable, then `dexcli`
   * on `PATH`.
   */
  readonly dexcliPath?: string;
  /**
   * Path to `dex-server-zd`. Defaults to `DEX_SERVER_ZD_BINARY`, then the file next to `dexcli`,
   * then the `dexcli` default.
   */
  readonly serverZdPath?: string;
  /**
   * Directory for engine state and logs. Defaults to a new temporary directory, which
   * {@link PrivateDexEngine.stop} deletes. A supplied directory is kept.
   */
  readonly stateDirectory?: string;
  /** Milliseconds to wait for the engine to report readiness. Defaults to 60,000. */
  readonly readyTimeoutMs?: number;
  /**
   * Path of a `dex-server-zd` YAML file with configuration overrides, passed as `dexcli dev -zd-config`.
   * Use it to test server settings, such as `streamStore: { backend: disabled }`, the production
   * default that drops Stream writes. Defaults to none.
   */
  readonly serverZdConfigPath?: string;
}

/**
 * A private `dexcli dev --engine zd` instance on free loopback ports, owned by one test process.
 *
 * Each instance has its own state directory and ports, so tests and other engines on the machine
 * never share state. The engine runs in its own process group; {@link stop} ends `dexcli` and
 * `dex-server-zd` together. Engines still running when the test process exits receive SIGKILL.
 *
 * @example
 * ```ts
 * import { before, after } from "node:test";
 * import { PrivateDexEngine } from "@superdurable/dex-agents/testing";
 *
 * let engine: PrivateDexEngine;
 * before(async () => { engine = await PrivateDexEngine.start(); });
 * after(async () => { await engine.stop(); });
 * ```
 */
export class PrivateDexEngine {
  private constructor(
    /** Dex Server gRPC address, `127.0.0.1:<port>`, for Client and Worker `serverAddress`. */
    public readonly serverAddress: string,
    /** Dex Web URL of this engine, useful when debugging a failing test. */
    public readonly webUrl: string,
    /** Directory holding the engine state, its log, and test-owned files. */
    public readonly stateDirectory: string,
    /** Path of the combined `dexcli` and `dex-server-zd` log. */
    public readonly logPath: string,
    private readonly processGroup: ChildProcessGroup,
    private readonly isStateDirectoryOwned: boolean,
  ) {}

  /**
   * Starts an engine and waits until it reports readiness.
   *
   * Ports are chosen by the operating system. A start that loses a port race is retried with new
   * ports, up to three times.
   *
   * @param options - Executable paths, state directory, and readiness timeout.
   * @returns The running engine.
   * @throws {Error} When `dexcli` cannot start or never reports readiness; the message holds its log.
   */
  public static async start(options: PrivateDexEngineOptions = {}): Promise<PrivateDexEngine> {
    const dexcliPath = options.dexcliPath ?? process.env.DEXCLI ?? "dexcli";
    const serverZdPath = options.serverZdPath ?? resolveServerZdPath(dexcliPath);
    const isStateDirectoryOwned = options.stateDirectory === undefined;
    const stateDirectory =
      options.stateDirectory ?? mkdtempSync(join(tmpdir(), "dex-agents-engine-"));
    mkdirSync(stateDirectory, { recursive: true });
    const logPath = join(stateDirectory, "dexcli.log");
    let lastFailure: unknown;
    for (let attempt = 1; attempt <= startAttempts; attempt += 1) {
      const [dexPort, webPort] = await findFreeLoopbackPorts(2);
      if (dexPort === undefined || webPort === undefined) {
        throw new Error("findFreeLoopbackPorts returned too few ports");
      }
      const commandArguments = [
        "dev",
        "-engine",
        "zd",
        "-bind-address",
        "127.0.0.1",
        "-dex-port",
        String(dexPort),
        "-web-port",
        String(webPort),
        "-open=false",
        "-zd-data-dir",
        join(stateDirectory, `zd-${attempt}`),
        "-connector-config-dir",
        join(stateDirectory, "connectors"),
      ];
      if (serverZdPath !== undefined) {
        commandArguments.push("-zd-binary", serverZdPath);
      }
      if (options.serverZdConfigPath !== undefined) {
        commandArguments.push("-zd-config", options.serverZdConfigPath);
      }
      try {
        const processGroup = await ChildProcessGroup.start({
          description: "dexcli dev",
          command: dexcliPath,
          commandArguments,
          environment: process.env,
          logPath,
          readyLine: engineReadyLine,
          readyTimeoutMs: options.readyTimeoutMs ?? 60_000,
        });
        return new PrivateDexEngine(
          `127.0.0.1:${dexPort}`,
          `http://127.0.0.1:${webPort}`,
          stateDirectory,
          logPath,
          processGroup,
          isStateDirectoryOwned,
        );
      } catch (error: unknown) {
        lastFailure = error;
      }
    }
    if (isStateDirectoryOwned) {
      rmSync(stateDirectory, { recursive: true, force: true });
    }
    throw new Error(`dexcli dev did not start after ${startAttempts} attempts`, {
      cause: lastFailure,
    });
  }

  /**
   * Returns the engine log written since it started.
   * @returns The combined `dexcli` and `dex-server-zd` output.
   */
  public readLog(): string {
    return this.processGroup.readLog();
  }

  /**
   * Stops the engine and, when the state directory was created by {@link start}, deletes it.
   *
   * Sends SIGTERM to the engine's process group and SIGKILL after 15 seconds. Calling it again is safe.
   * @returns A Promise that resolves after the engine exited.
   */
  public async stop(): Promise<void> {
    await this.processGroup.stop();
    if (this.isStateDirectoryOwned) {
      rmSync(this.stateDirectory, { recursive: true, force: true });
    }
  }
}

/**
 * Returns a TCP port on 127.0.0.1 that was free when checked.
 *
 * Another process can still take the port before it is used, so callers retry on bind failures.
 * Use {@link findFreeLoopbackPorts} when a caller needs several ports at once.
 * @returns A port number chosen by the operating system.
 */
export async function findFreeLoopbackPort(): Promise<number> {
  const [port] = await findFreeLoopbackPorts(1);
  if (port === undefined) {
    throw new Error("no loopback port was chosen");
  }
  return port;
}

/**
 * Returns distinct TCP ports on 127.0.0.1 that were free when checked.
 *
 * Every listener stays open until all ports are chosen, so the operating system never hands out
 * the same port twice. Another process can still take a port after the listeners close, so callers
 * retry on bind failures.
 * @param count - Number of ports, at least 1.
 * @returns `count` distinct port numbers chosen by the operating system.
 * @throws {RangeError} When `count` is not a positive integer.
 */
export async function findFreeLoopbackPorts(count: number): Promise<number[]> {
  if (!Number.isSafeInteger(count) || count <= 0) {
    throw new RangeError(`port count must be a positive integer: ${count}`);
  }
  const servers: Server[] = [];
  try {
    const ports: number[] = [];
    for (let index = 0; index < count; index += 1) {
      const server = createServer();
      servers.push(server);
      ports.push(await listenOnFreeLoopbackPort(server));
    }
    return ports;
  } finally {
    await Promise.all(servers.map((server) => closeServer(server)));
  }
}

function listenOnFreeLoopbackPort(server: Server): Promise<number> {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        reject(new Error("loopback listener has no port"));
        return;
      }
      resolve(address.port);
    });
  });
}

function closeServer(server: Server): Promise<void> {
  return new Promise((resolve) => {
    if (!server.listening) {
      resolve();
      return;
    }
    server.close(() => resolve());
  });
}

function resolveServerZdPath(dexcliPath: string): string | undefined {
  const fromEnvironment = process.env.DEX_SERVER_ZD_BINARY;
  if (fromEnvironment !== undefined && fromEnvironment !== "") {
    return fromEnvironment;
  }
  if (!dexcliPath.includes("/")) {
    return undefined;
  }
  const sibling = join(dirname(dexcliPath), "dex-server-zd");
  return existsSync(sibling) ? sibling : undefined;
}
