// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Metadata, credentials } from "@grpc/grpc-js";

import type { BlobCache } from "./blob-cache.js";
import { credentialInterceptors } from "./credential.js";
import { FlowServiceClient, type ServerInfo } from "./gen/dex.js";
import { registeredAttributeIndexes, type Registry } from "./flow.js";
import type { WorkerOptions } from "./options.js";
import { negotiateServerProtocol, serverInfoRequestError } from "./server-protocol.js";
import { sdkVersion } from "./sdk-version.generated.js";
import { ValueHydrator } from "./value-mapper.js";
import { WorkerDispatcher } from "./worker-dispatcher.js";
import { PullWorker } from "./pull-worker.js";

export { PullRegistrationError } from "./pull-registration.js";
export type { PullRegistrationState, PullQueueRegistrationState, WaitPullRegisteredOptions } from "./pull-registration.js";
import type { PullRegistrationState, WaitPullRegisteredOptions } from "./pull-registration.js";

type WorkerState = "created" | "running" | "stopping" | "stopped" | "closed";

/**
 * Hosts registered Step and RPC handlers through outbound pull streams.
 *
 * A Worker is one-shot. Call `start` for non-blocking startup or `run` to await
 * shutdown, then call `close`. Concurrent handlers must synchronize shared state.
 * `close` drains for `stopDrainTimeoutMs`, then cancels handlers and
 * waits for them to return. Half-closed streams wait for server EOF until the
 * remaining drain deadline. A handler that ignores cancellation can delay shutdown.
 *
 * @example
 * ```ts
 * const worker = new Worker(registry, cache);
 * await worker.start();
 * try { await worker.waitPullRegistered({ timeoutMs: 10_000 }); } finally { await worker.close(); }
 * ```
 */
export class Worker {
  private readonly flowService: InstanceType<typeof FlowServiceClient>;
  private readonly pullWorker: PullWorker;
  private readonly stopped: Promise<void>;
  private resolveStopped!: () => void;
  private state: WorkerState = "created";
  private negotiatedProtocolVersion = 0;
  private shutdownError: unknown;

  /**
   * Constructs a Worker without starting its task streams. Workers need only an outbound connection to the Dex Server.
   * @param registry - Flow definitions served by this Worker.
   * @param blobCache - Open cache used to hydrate large handler values.
   * @param options - Server connectivity, task capacity, bearer credential, and startup settings; uses local defaults when omitted.
   * @throws {TypeError} When the Worker ID is invalid.
   * @throws {RangeError} When a pull capacity or drain timeout is invalid.
   */
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: WorkerOptions = {},
  ) {
    this.flowService = new FlowServiceClient(
      options.serverAddress ?? "localhost:8801",
      credentials.createInsecure(),
      { interceptors: credentialInterceptors(options.credential) },
    );
    const dispatcher = new WorkerDispatcher(
      registry,
      new ValueHydrator(this.flowService, blobCache),
      () => this.negotiatedProtocolVersion,
    );
    try {
      this.pullWorker = new PullWorker(
        registry, dispatcher, options.serverAddress ?? "localhost:8801", options.pull ?? {},
        options.credential, () => this.stopAfterPermanentRejection(),
      );
    } catch (failure) {
      this.flowService.close();
      throw failure;
    }
    this.stopped = new Promise((resolve) => {
      this.resolveStopped = resolve;
    });
  }

  /**
   * Negotiates the Server protocol, synchronizes indexes, and starts polling.
   * The promise resolves after launching pull streams; call exactly once.
   * Await waitPullRegistered separately to gate startup on pull queue acknowledgements.
   */
  public async start(): Promise<void> {
    if (this.state !== "created") {
      throw new Error(`Worker cannot start from state ${this.state}`);
    }
    try {
      const timeoutMs = this.options.attributeIndexSyncTimeoutMs ?? 120_000;
      if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
        throw new RangeError("attributeIndexSyncTimeoutMs must be positive");
      }
      let serverInfo: ServerInfo;
      try {
        serverInfo = await getServerInfo(this.flowService, timeoutMs);
      } catch (failure) {
        throw serverInfoRequestError(sdkVersion, failure);
      }
      this.negotiatedProtocolVersion = negotiateServerProtocol(serverInfo, sdkVersion);
      await syncAttributeIndexes(this.flowService, this.registry, timeoutMs);
      this.pullWorker.start();
      this.state = "running";
    } catch (failure) {
      const endpoint = `pull Worker for ${this.options.serverAddress ?? "localhost:8801"}`;
      const startupError = new Error(`cannot start TypeScript ${endpoint}`, { cause: failure });
      this.pullWorker.registration.stopped(startupError);
      this.state = "stopped";
      await this.pullWorker.close();
      this.flowService.close();
      this.resolveStopped();
      throw startupError;
    }
  }

  /**
   * Starts the Worker and waits until `close` completes shutdown.
   * @returns A promise resolved after graceful shutdown and handler completion.
   * @throws {Error} Startup or shutdown fails.
   * @throws {PullRegistrationError} A permanent rejection leaves a queue without registered streams when stopOnPermanentStreamRejection is enabled.
   */
  public async run(): Promise<void> {
    await this.start();
    await this.stopped;
    if (this.shutdownError !== undefined) throw this.shutdownError;
    if (this.options.pull?.stopOnPermanentStreamRejection === true && this.pullWorker.registration.permanentError !== undefined) {
      throw this.pullWorker.registration.permanentError;
    }
  }

  /**
   * Waits until every Flow type queue and the app Worker ID queue has registered at least once.
   * Safe before or after start and for concurrent callers. Later calls resolve even after shutdown.
   * Reconnects do not reset completion. Use pullRegistrationState for ongoing readiness probes.
   * @param options - Optional AbortSignal and positive timeout in milliseconds; defaults to waiting indefinitely.
   * @returns A promise resolved after all queues have registered once.
   * @throws {PullRegistrationError} A permanent rejection leaves a queue without another registered stream before readiness.
   * @throws {Error} Startup fails or the Worker stops before readiness.
   * @throws {RangeError} The timeout is invalid.
   * @throws {DOMException} The timeout expires; cancellation rejects with the signal's reason.
   * @example
   * ```ts
   * await worker.start();
   * await worker.waitPullRegistered({ timeoutMs: 10_000 });
   * console.log(worker.pullRegistrationState().isReady);
   * ```
   */
  public waitPullRegistered(options: WaitPullRegisteredOptions = {}): Promise<void> {
    return this.pullWorker.registration.wait(options);
  }

  /**
   * Cumulative Step declines caused by reserved RPC capacity during this Worker's lifetime.
   * Counts decline events. Dex may decline the same Step dispatch repeatedly while retrying it,
   * so read the counter as a rate. Excludes full-capacity rejections and unknown names. The count never resets and
   * remains available after close. Reading during
   * handler execution is safe. Export the value to a monitoring counter.
   * @returns The cumulative reserve-decline count.
   * @example
   * ```ts
   * const declines = worker.pullReservedRpcDeclines;
   * ```
   */
  public get pullReservedRpcDeclines(): number {
    return this.pullWorker.reservedRpcDeclines;
  }

  /**
   * Returns an independent snapshot of pull queue registrations.
   * Readiness latches after every queue registers once. Only a permanent rejection with no
   * registered stream makes it false; re-registration clears the queue error. Stop preserves readiness.
   * @returns Current readiness and queue snapshots.
   * See waitPullRegistered for a startup and readiness example. Treat errors as immutable.
   */
  public pullRegistrationState(): PullRegistrationState {
    return this.pullWorker.registration.snapshot();
  }

  private stopAfterPermanentRejection(): void {
    this.close().catch((failure: unknown) => {
      this.shutdownError = failure;
      this.resolveStopped();
    });
  }

  /**
   * Gracefully drains tasks, then releases connections.
   * A pull Worker force-cancels after its drain timeout and waits for handlers to return.
   * Half-closed pull streams wait for server EOF until the remaining drain deadline.
   * Calls before `start` and repeated calls after shutdown are safe.
   */
  public async close(): Promise<void> {
    if (this.state === "closed") {
      return;
    }
    if (this.state === "created") {
      this.state = "closed";
      await this.pullWorker.close();
      this.flowService.close();
      this.resolveStopped();
      return;
    }
    if (this.state === "running") {
      this.state = "stopping";
      try {
        await this.pullWorker.close();
      } finally {
        this.flowService.close();
        this.state = "stopped";
        this.resolveStopped();
      }
    } else {
      await this.stopped;
    }
    this.state = "closed";
  }
}

function getServerInfo(
  service: InstanceType<typeof FlowServiceClient>,
  timeoutMs: number,
): Promise<ServerInfo> {
  return new Promise((resolve, reject) => {
    service.getServerInfo({}, new Metadata(), { deadline: Date.now() + timeoutMs }, (error, info) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve(info);
    });
  });
}

function syncAttributeIndexes(
  service: InstanceType<typeof FlowServiceClient>,
  registry: Registry,
  timeoutMs: number,
): Promise<void> {
  return new Promise((resolve, reject) => {
    service.syncAttributeIndexes(
      { attributeIndexes: Object.fromEntries(registeredAttributeIndexes(registry)) },
      new Metadata(),
      { deadline: Date.now() + timeoutMs },
      (error) => {
        if (error !== null) {
          reject(error);
          return;
        }
        resolve();
      },
    );
  });
}
