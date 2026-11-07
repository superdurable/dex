// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Metadata } from "@grpc/grpc-js";

import type { BlobCache } from "./blob-cache.js";
import { credentialInterceptors } from "./credential.js";
import { createFlowServiceChannel } from "./flow-service-tls.js";
import { FlowServiceClient, type ServerInfo } from "./gen/dex.js";
import { registeredAttributeIndexes, type Registry } from "./flow.js";
import type { WorkerOptions } from "./options.js";
import { negotiateServerProtocol } from "./server-protocol.js";
import { sdkVersion } from "./sdk-version.generated.js";
import { ValueHydrator } from "./value-mapper.js";
import { WorkerDispatcher } from "./worker-dispatcher.js";
import { PullWorker } from "./pull-worker.js";

export { PullRegistrationError } from "./pull-registration.js";
export type { PullRegistrationState, PullQueueRegistrationState, WaitPullRegisteredOptions } from "./pull-registration.js";
import { PullRegistrationError, type PullRegistrationState, type WaitPullRegisteredOptions } from "./pull-registration.js";

type WorkerState = "created" | "starting" | "running" | "stopping" | "stopped" | "closed";

/**
 * Hosts registered Step and RPC handlers through outbound pull streams.
 *
 * A Worker is one-shot. Call `start` to register the first slot or `run` to await
 * shutdown, then call `close`. Concurrent handlers must synchronize shared state.
 * `close` stops new tasks, half-closes idle streams, and lets busy handlers send final output before half-closing.
 * It drains for `stopDrainTimeoutMs`, then resets streams whose handler has not written final output,
 * cancels those handlers, and waits for them to return. Half-closed streams wait for server EOF until
 * the drain deadline and are then reset. A stream whose final output Dex has not yet confirmed, by ending
 * the stream or assigning its next task, gets 5 more seconds, so `close` can return up to 5 seconds later.
 * A handler that ignores cancellation can delay shutdown.
 * Changed in SDK v1.35.0: reserved RPC slots and protocol 4 require server-zd v0.1.17 or later.
 * Upgrade the Server first. See PullWorkerOptions for defaults, concurrency caps, and slot selection.
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
   * Changed in 1.38.0: the default gRPC receive limit rose from 4 MiB to
   * `DEFAULT_GRPC_MAX_MESSAGE_BYTES` on FlowService calls and task streams, and a LoadBlobs
   * batch over the SDK's gRPC limit is split instead of failing.
   * @param registry - Flow definitions served by this Worker.
   * @param blobCache - Open cache used to hydrate large handler values.
   * @param options - Server connectivity, task capacity, bearer credential, startup settings, and gRPC message size limit; uses local defaults when omitted.
   * @throws {TypeError} When the Worker ID is invalid, or `flowServiceTls` is set and the address or TLS settings are invalid.
   * @throws {RangeError} When a pull capacity or drain timeout is invalid, or `grpcMaxMessageBytes` is negative or not an integer.
   */
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: WorkerOptions = {},
  ) {
    const channel = createFlowServiceChannel(
      options.serverAddress ?? "localhost:8801",
      options.flowServiceTls,
      options.grpcMaxMessageBytes,
    );
    this.flowService = new FlowServiceClient(
      channel.target,
      channel.channelCredentials,
      { ...channel.channelOptions, interceptors: credentialInterceptors(options.credential) },
    );
    const dispatcher = new WorkerDispatcher(
      registry,
      new ValueHydrator(this.flowService, blobCache),
      () => this.negotiatedProtocolVersion,
    );
    try {
      this.pullWorker = new PullWorker(
        registry, dispatcher, channel, options.pull ?? {},
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
   * The promise resolves after the first slot registers and its Server concurrency limit is checked; call exactly once.
   * Await waitPullRegistered separately to gate readiness on both configured slot kinds.
   * @returns A promise resolved after protocol negotiation, index synchronization, and the first registration.
   * @throws {Error} The Server is incompatible, unreachable (the gRPC error is the cause), or startup fails.
   * @throws {Error} maxConcurrentTasks exceeds matching.maxConcurrentTasksCap; the Worker half-closes its streams.
   * @throws {PullRegistrationError} The Server permanently rejects every stream before any registers.
   */
  public async start(): Promise<void> {
    if (this.state !== "created") {
      throw new Error(`Worker cannot start from state ${this.state}`);
    }
    this.state = "starting";
    try {
      const timeoutMs = this.options.attributeIndexSyncTimeoutMs ?? 120_000;
      if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
        throw new RangeError("attributeIndexSyncTimeoutMs must be positive");
      }
      const serverInfo = await getServerInfo(this.flowService, timeoutMs);
      this.negotiatedProtocolVersion = negotiateServerProtocol(serverInfo, sdkVersion);
      await syncAttributeIndexes(this.flowService, this.registry, timeoutMs);
      this.pullWorker.start();
      await this.pullWorker.waitStarted();
      if (this.state !== "starting") throw new Error("Worker stopped during startup");
      this.state = "running";
    } catch (failure) {
      const endpoint = `pull Worker for ${this.options.serverAddress ?? "localhost:8801"}`;
      const startupError = failure instanceof PullRegistrationError ? failure
        : new Error(`cannot start TypeScript ${endpoint}: ${failure instanceof Error ? failure.message : String(failure)}`, { cause: failure });
      this.pullWorker.registration.stopped(startupError);
      // A concurrent close already released resources and owns the final state.
      if (this.state === "starting") {
        this.state = "stopped";
        await this.pullWorker.close();
        this.flowService.close();
        this.resolveStopped();
      }
      throw startupError;
    }
  }

  /**
   * Starts the Worker and waits until `close` completes shutdown.
   * @returns A promise resolved after graceful shutdown and handler completion.
   * @throws {Error} Startup or shutdown fails.
   * @throws {PullRegistrationError} A permanent rejection leaves a slot kind without registered streams when stopOnPermanentStreamRejection is enabled.
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
   * Waits until at least one STEP_AND_RPC stream and, when reserved, one RPC_ONLY stream have registered.
   * Safe before or after start and for concurrent callers. Later calls resolve even after shutdown.
   * Reconnects do not reset completion. Use pullRegistrationState for ongoing readiness probes.
   * @param options - Optional AbortSignal and positive timeout in milliseconds; defaults to waiting indefinitely.
   * @returns A promise resolved after all configured slot kinds have registered once.
   * @throws {PullRegistrationError} A permanent rejection leaves a slot kind without another registered stream before readiness.
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
   * Returns the retained reserved RPC decline counter.
   * Changed in SDK v1.35.0: reserved slots accept only RPCs and this counter is always zero.
   * @deprecated Reserved slots never decline Steps for capacity. Always returns zero, including after close.
   * @returns The constant value zero.
   * @example
   * ```ts
   * const legacyCounter = worker.pullReservedRpcDeclines;
   * ```
   */
  public get pullReservedRpcDeclines(): number {
    return this.pullWorker.reservedRpcDeclines;
  }

  /**
   * Returns an independent snapshot of slot-kind registrations.
   * Readiness latches after every configured slot kind registers once. Only a permanent rejection with no
   * registered stream makes it false; re-registration clears the slot-kind error. Stop preserves readiness.
   * @returns Current readiness and slot-kind snapshots.
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
   * Stops new tasks and drains handlers before releasing connections.
   * Idle streams half-close immediately. Busy streams send their final output before half-closing.
   * Tasks arriving after stop begins are dropped without running a handler, including after half-close.
   * Streams keep reading until the Server ends them.
   * After the drain timeout, streams whose handler has not written final output are reset, those handlers
   * are canceled, and close waits for them to return. A handler whose final output is written still half-closes.
   * Half-closed streams that the Server has not ended by the drain deadline are reset.
   * A stream whose final output the Server has not yet confirmed gets a 5-second flush grace after the deadline.
   * The Server confirms it by ending the stream or assigning the slot's next task.
   * Close can therefore return up to 5 seconds after the drain deadline.
   * Dex treats a task dropped on a reset stream as possibly run.
   * Changed in SDK v1.35.0: a graceful half-close returns unstarted tasks without consuming a retry.
   * Running handlers can finish and send output; a forced reset or disconnect loses this guarantee.
   * Calls before `start` and repeated calls after shutdown are safe.
   */
  public async close(): Promise<void> {
    if (this.state === "closed") {
      return;
    }
    if (this.state === "created" || this.state === "starting") {
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
