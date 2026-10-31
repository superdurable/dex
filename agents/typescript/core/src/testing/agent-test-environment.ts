// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  Client,
  Registry,
  Worker,
  openBlobCache,
  type BlobCache,
  type Flow,
} from "@superdurable/dex";

/** Options of {@link AgentTestEnvironment.start}. */
export interface AgentTestEnvironmentOptions {
  /** Dex Server address, usually {@link PrivateDexEngine.serverAddress}. */
  readonly serverAddress: string;
  /** Flows registered in the environment's Registry. */
  readonly flows: readonly Flow<any>[];
  /**
   * Whether to run a pull Worker in this process. Pass `false` when the Worker runs in a
   * {@link KillableWorkerProcess}; the Client still needs the same Flows in its Registry.
   */
  readonly isWorkerInProcess: boolean;
}

/**
 * A Registry, BlobCache, Client, and optional in-process Worker for one test file.
 *
 * @example
 * ```ts
 * const environment = await AgentTestEnvironment.start({
 *   serverAddress: engine.serverAddress, flows: [flow], isWorkerInProcess: true });
 * try {
 *   const sessions = new AgentSessionTestClient(environment.client, flow);
 *   // ...
 * } finally {
 *   await environment.close();
 * }
 * ```
 */
export class AgentTestEnvironment {
  private constructor(
    /** Registry shared by the Client and the in-process Worker. */
    public readonly registry: Registry,
    /** Client connected to the server. */
    public readonly client: Client,
    /** In-process Worker, or `undefined` when the Worker runs elsewhere. */
    public readonly worker: Worker | undefined,
    private readonly blobCache: BlobCache,
    private readonly cacheDirectory: string,
  ) {}

  /**
   * Creates the environment and, when requested, starts the Worker and waits for its registration.
   * @param options - Server address, Flows, and Worker placement.
   * @returns The ready environment.
   * @throws {FlowDefinitionError} When the Flows are invalid.
   * @throws {Error} When the Worker cannot register within 30 seconds.
   */
  public static async start(options: AgentTestEnvironmentOptions): Promise<AgentTestEnvironment> {
    const registry = new Registry(options.flows);
    const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-agents-test-cache-"));
    const blobCache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1024 * 1024 });
    let worker: Worker | undefined;
    if (options.isWorkerInProcess) {
      worker = new Worker(registry, blobCache, {
        serverAddress: options.serverAddress,
        // Parallel tool Steps must not depend on the CI machine's CPU count.
        pull: { appWorkerID: `dex-agents-test-worker-${randomUUID()}`, maxConcurrentTasks: 8 },
      });
      await worker.start();
      await worker.waitPullRegistered({ timeoutMs: 30_000 });
    }
    const client = new Client(registry, blobCache, { serverAddress: options.serverAddress });
    return new AgentTestEnvironment(registry, client, worker, blobCache, cacheDirectory);
  }

  /**
   * Closes the Client, the in-process Worker, and the BlobCache, then deletes the cache directory.
   * @returns A Promise that resolves after everything closed.
   */
  public async close(): Promise<void> {
    try {
      await this.client.close();
      await this.worker?.close();
      this.blobCache.close();
    } finally {
      rmSync(this.cacheDirectory, { recursive: true, force: true });
    }
  }
}
