// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomUUID } from "node:crypto";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { Registry, Worker, openBlobCache, type Flow } from "@superdurable/dex";

import { ChildProcessGroup } from "./child-process-group.js";

/** Environment variable that carries the Dex Server address into a Worker child process. */
export const agentWorkerServerAddressVariable = "DEX_AGENTS_SERVER_ADDRESS";
/**
 * Environment variable naming the BlobCache directory of a Worker child process. When it is unset,
 * the child uses a new temporary directory and deletes it on exit.
 */
export const agentWorkerBlobCacheDirectoryVariable = "DEX_AGENTS_BLOB_CACHE_DIR";
/** Line a Worker child process prints once every queue is registered; the same as the Python core. */
export const agentWorkerReadyLine = "dex-agents test Worker ready";

/** Options of {@link KillableWorkerProcess.start}. */
export interface KillableWorkerProcessOptions {
  /**
   * Path of the compiled ES module the child runs with `node`. The module must call
   * {@link runAgentWorkerProcess} with the Flows it serves.
   */
  readonly entryModulePath: string;
  /** Dex Server address, usually {@link PrivateDexEngine.serverAddress}. */
  readonly serverAddress: string;
  /** Log file; output of every start is appended to it. */
  readonly logPath: string;
  /** Extra environment variables for the child, such as a directory for tool marker files. */
  readonly environment?: Readonly<Record<string, string>>;
  /** Milliseconds to wait for {@link agentWorkerReadyLine}. Defaults to 60,000. */
  readonly readyTimeoutMs?: number;
}

/**
 * A Dex Worker running in a child process that a test can hard-kill.
 *
 * The child leads its own process group. {@link kill} sends SIGKILL to the group, so the Worker
 * gets no chance to drain, finish a Step, or close its streams, exactly like a crashed host.
 * Start a new instance afterward to model the replacement Worker.
 *
 * @example
 * ```ts
 * // worker-main.ts, compiled to dist/test/worker-main.js
 * import { runAgentWorkerProcess } from "@superdurable/dex-agents/testing";
 * await runAgentWorkerProcess([createMyAgentFlow()]);
 *
 * // In the test
 * const worker = await KillableWorkerProcess.start({
 *   entryModulePath: fileURLToPath(new URL("./worker-main.js", import.meta.url)),
 *   serverAddress: engine.serverAddress,
 *   logPath: join(engine.stateDirectory, "worker.log"),
 * });
 * await worker.kill();
 * ```
 */
export class KillableWorkerProcess {
  private constructor(private readonly processGroup: ChildProcessGroup) {}

  /**
   * Starts the child and waits until its Worker registered every pull queue.
   * @param options - Entry module, server address, log path, and environment.
   * @returns The running Worker process.
   * @throws {Error} When the child exits or is not ready in time; the message holds its log.
   */
  public static async start(options: KillableWorkerProcessOptions): Promise<KillableWorkerProcess> {
    const processGroup = await ChildProcessGroup.start({
      description: `Worker ${options.entryModulePath}`,
      command: process.execPath,
      commandArguments: [options.entryModulePath],
      environment: {
        ...process.env,
        ...options.environment,
        [agentWorkerServerAddressVariable]: options.serverAddress,
      },
      logPath: options.logPath,
      readyLine: agentWorkerReadyLine,
      readyTimeoutMs: options.readyTimeoutMs ?? 60_000,
    });
    return new KillableWorkerProcess(processGroup);
  }

  /** Process ID of the child. */
  public get pid(): number {
    return this.processGroup.pid;
  }

  /** Whether the child is still running. */
  public get isRunning(): boolean {
    return this.processGroup.isRunning;
  }

  /**
   * Returns the child's output since this start.
   * @returns Standard output and standard error, interleaved.
   */
  public readLog(): string {
    return this.processGroup.readLog();
  }

  /**
   * Sends SIGKILL to the child's process group and waits for it to exit. Safe to call again.
   * @returns A Promise that resolves after the child exited.
   */
  public kill(): Promise<void> {
    return this.processGroup.kill();
  }

  /**
   * Asks the child to drain and exit with SIGTERM, then sends SIGKILL after 15 seconds.
   * @returns A Promise that resolves after the child exited.
   */
  public stop(): Promise<void> {
    return this.processGroup.stop();
  }
}

/**
 * Runs a pull Worker for the given Flows until SIGTERM or SIGINT; call it from a Worker child module.
 *
 * It reads the server address from {@link agentWorkerServerAddressVariable} and the BlobCache directory
 * from {@link agentWorkerBlobCacheDirectoryVariable}, uses a fresh app Worker ID, waits for pull
 * registration, then prints {@link agentWorkerReadyLine} to standard output. On SIGTERM or SIGINT it
 * closes the Worker and exits with status 0.
 *
 * @param flows - Flows the Worker serves; build them exactly as the test's Client Registry does.
 * @returns A Promise that never resolves; the process exits on a signal.
 * @throws {Error} When the server address is missing or the Worker cannot register.
 */
export async function runAgentWorkerProcess(flows: readonly Flow<any>[]): Promise<never> {
  const serverAddress = process.env[agentWorkerServerAddressVariable];
  if (serverAddress === undefined || serverAddress === "") {
    throw new Error(`${agentWorkerServerAddressVariable} is not set`);
  }
  const configuredCacheDirectory = process.env[agentWorkerBlobCacheDirectoryVariable];
  const isCacheDirectoryOwned =
    configuredCacheDirectory === undefined || configuredCacheDirectory === "";
  const cacheDirectory = isCacheDirectoryOwned
    ? mkdtempSync(join(tmpdir(), "dex-agents-worker-cache-"))
    : configuredCacheDirectory;
  mkdirSync(cacheDirectory, { recursive: true });
  const blobCache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1024 * 1024 });
  const worker = new Worker(new Registry(flows), blobCache, {
    serverAddress,
    pull: { appWorkerID: `dex-agents-test-worker-${randomUUID()}` },
  });
  const closeAndExit = async (): Promise<void> => {
    try {
      await worker.close();
      blobCache.close();
    } finally {
      if (isCacheDirectoryOwned) {
        rmSync(cacheDirectory, { recursive: true, force: true });
      }
      process.exit(0);
    }
  };
  process.once("SIGTERM", () => void closeAndExit());
  process.once("SIGINT", () => void closeAndExit());
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 30_000 });
  process.stdout.write(`${agentWorkerReadyLine}\n`);
  return new Promise<never>(() => undefined);
}
