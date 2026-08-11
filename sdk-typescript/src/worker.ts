// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {
  Server,
  ServerCredentials,
  Metadata,
  credentials,
  type sendUnaryData,
} from "@grpc/grpc-js";

import type { BlobCache } from "./blob-cache.js";
import {
  FlowServiceClient,
  WorkerServiceService,
  type InvokeExecuteMethodRequest,
  type InvokeExecuteMethodResponse,
  type InvokeWaitForMethodRequest,
  type InvokeWaitForMethodResponse,
  type InvokeWorkerRPCRequest,
  type InvokeWorkerRPCResponse,
  type WorkerServiceServer,
} from "./gen/dex.js";
import { workerServiceError } from "./grpc-status.js";
import { registeredAttributeIndexes, type Registry } from "./flow.js";
import type { WorkerOptions, WorkerTarget } from "./options.js";
import { ValueHydrator } from "./value-mapper.js";
import { WorkerDispatcher } from "./worker-dispatcher.js";

type WorkerState = "created" | "running" | "stopping" | "stopped" | "closed";

/**
 * Hosts registered Step and RPC handlers over the private WorkerService protocol.
 *
 * A Worker is one-shot. Call `start` for non-blocking startup or `run` to await
 * shutdown, then call `close`. Concurrent handlers must synchronize shared state.
 *
 * @example
 * ```ts
 * const worker = new Worker(registry, cache);
 * await worker.start();
 * try { console.log(worker.workerTarget.address); } finally { await worker.close(); }
 * ```
 */
export class Worker {
  /** Effective endpoint advertised to Dex. */
  public readonly workerTarget: WorkerTarget;

  private readonly server = new Server();
  private readonly flowService: InstanceType<typeof FlowServiceClient>;
  private readonly stopped: Promise<void>;
  private resolveStopped!: () => void;
  private state: WorkerState = "created";

  /**
   * Constructs a Worker without starting its listener.
   * @param registry - Flow definitions served by this Worker.
   * @param blobCache - Open cache used to hydrate large handler values.
   * @param options - Networking and startup settings; uses local defaults when omitted.
   */
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: WorkerOptions = {},
  ) {
    const bindAddress = options.bindAddress ?? "127.0.0.1:8803";
    this.workerTarget = options.workerTarget ?? targetFromBindAddress(bindAddress);
    this.flowService = new FlowServiceClient(
      options.serverAddress ?? "localhost:8801",
      credentials.createInsecure(),
    );
    const dispatcher = new WorkerDispatcher(
      registry,
      new ValueHydrator(this.flowService, blobCache),
    );
    this.server.addService(WorkerServiceService, workerService(dispatcher));
    this.stopped = new Promise((resolve) => {
      this.resolveStopped = resolve;
    });
  }

  /**
   * Synchronizes Attribute indexes and starts the WorkerService listener.
   * The promise resolves after successful binding; call exactly once.
   */
  public async start(): Promise<void> {
    if (this.state !== "created") {
      throw new Error(`Worker cannot start from state ${this.state}`);
    }
    const bindAddress = this.options.bindAddress ?? "127.0.0.1:8803";
    try {
      const timeoutMs = this.options.attributeIndexSyncTimeoutMs ?? 120_000;
      if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
        throw new RangeError("attributeIndexSyncTimeoutMs must be positive");
      }
      await syncAttributeIndexes(this.flowService, this.registry, timeoutMs);
      await bind(this.server, bindAddress);
      this.state = "running";
    } catch (failure) {
      this.state = "stopped";
      this.flowService.close();
      this.resolveStopped();
      throw new Error(`cannot start TypeScript Worker on ${bindAddress}`, { cause: failure });
    }
  }

  /** Starts the Worker and waits until `close` completes shutdown. */
  public async run(): Promise<void> {
    await this.start();
    await this.stopped;
  }

  /**
   * Gracefully stops the listener and releases the FlowService connection.
   * Calls before `start` and repeated calls after shutdown are safe.
   */
  public async close(): Promise<void> {
    if (this.state === "closed") {
      return;
    }
    if (this.state === "created") {
      this.state = "closed";
      this.flowService.close();
      this.resolveStopped();
      return;
    }
    if (this.state === "running") {
      this.state = "stopping";
      await shutdown(this.server);
      this.flowService.close();
      this.state = "stopped";
      this.resolveStopped();
    } else {
      await this.stopped;
    }
    this.state = "closed";
  }
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

function workerService(dispatcher: WorkerDispatcher): WorkerServiceServer {
  return {
    invokeWaitForMethod(call, callback) {
      invoke(() => dispatcher.invokeWaitFor(call.request), callback);
    },
    invokeExecuteMethod(call, callback) {
      invoke(() => dispatcher.invokeExecute(call.request), callback);
    },
    invokeWorkerRpc(call, callback) {
      invoke(() => dispatcher.invokeRPC(call.request), callback);
    },
  };
}

function invoke<Response>(
  invocation: () => Promise<Response>,
  callback: sendUnaryData<Response>,
): void {
  invocation().then(
    (response) => callback(null, response),
    (failure: unknown) => callback(workerServiceError(failure), null),
  );
}

function bind(server: Server, address: string): Promise<number> {
  parseAddress(address);
  return new Promise((resolve, reject) => {
    server.bindAsync(address, ServerCredentials.createInsecure(), (error, port) => {
      if (error !== null) {
        reject(error);
        return;
      }
      resolve(port);
    });
  });
}

function shutdown(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.tryShutdown((error) => {
      if (error !== undefined) {
        reject(error);
        return;
      }
      resolve();
    });
  });
}

function targetFromBindAddress(address: string): WorkerTarget {
  const parsed = parseAddress(address);
  const host = parsed.host === "" || parsed.host === "0.0.0.0" || parsed.host === "::"
    ? "localhost"
    : parsed.host;
  return { address: host.includes(":") ? `[${host}]:${parsed.port}` : `${host}:${parsed.port}` };
}

function parseAddress(address: string): { host: string; port: number } {
  if (address.trim() !== address || address === "") {
    throw new TypeError("Worker bind address is required without whitespace");
  }
  const bracketed = /^\[([^\]]+)]:(\d+)$/.exec(address);
  const separator = address.lastIndexOf(":");
  const host = bracketed?.[1] ?? (separator < 0 ? "" : address.slice(0, separator));
  const portText = bracketed?.[2] ?? (separator < 0 ? "" : address.slice(separator + 1));
  const port = Number(portText);
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new TypeError("Worker bind address requires port 1-65535");
  }
  return { host, port };
}
