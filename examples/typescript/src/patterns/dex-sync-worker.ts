/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type MessagePort, workerData } from "node:worker_threads";

import {
  Client,
  DexError,
  ErrorSubStatus,
  LongPollTimeoutError,
  Registry,
  openBlobCache,
  voidCodec,
  type Flow,
} from "@superdurable/dex";

import {
  notifySyncDone,
  type SyncClientRequest,
  type SyncClientResponse,
} from "./client-sync.js";
import { createDesignPatternRegistry } from "./design-pattern-registry.js";

interface WorkerData {
  readonly serverAddress: string;
  readonly request: SyncClientRequest;
  readonly port: MessagePort;
  readonly lock: Int32Array;
  readonly cacheDirectory?: string;
  readonly workerTargetAddress?: string;
}

async function run(): Promise<void> {
  const {
    serverAddress,
    request,
    port,
    lock,
    cacheDirectory,
    workerTargetAddress,
  } = workerData as WorkerData;
  const directory =
    cacheDirectory ?? mkdtempSync(join(tmpdir(), "dex-typescript-examples-sync-"));
  const blobCache = openBlobCache({
    directory,
    maxBytes: 64 * 1024 * 1024,
  });
  const registry = createDesignPatternRegistry();
  const client = new Client(registry, blobCache, {
    serverAddress,
    ...(workerTargetAddress !== undefined && workerTargetAddress.length > 0
      ? { workerTarget: { address: workerTargetAddress } }
      : {}),
  });
  try {
    const response = await handle(client, registry, request);
    port.postMessage(response);
  } catch (failure) {
    port.postMessage(toErrorResponse(failure));
  } finally {
    await client.close();
    blobCache.close();
    port.close();
    notifySyncDone(lock);
  }
}

async function handle(
  client: Client,
  registry: Registry,
  request: SyncClientRequest,
): Promise<SyncClientResponse> {
  switch (request.kind) {
    case "startFlow": {
      const flow = findFlow(registry, request.flowType);
      await client.startFlow(flow, request.flowId, request.input as never, request.options);
      return { ok: true };
    }
    case "waitForFlow": {
      try {
        await client.waitForFlow(request.flowId, voidCodec, request.timeoutMs);
        return { ok: true };
      } catch (failure) {
        if (failure instanceof LongPollTimeoutError) {
          return {
            ok: false,
            error: failure.message,
            subStatus: ErrorSubStatus.LONG_POLL_TIMEOUT,
          };
        }
        throw failure;
      }
    }
    case "invokeRpc": {
      const flow = findFlow(registry, request.flowType);
      const rpcMethod = resolveRpcMethod(flow, request.rpcName);
      if (rpcMethod === undefined) {
        return { ok: false, error: `unknown rpc ${request.rpcName}` };
      }
      const result =
        request.input === undefined
          ? await client.invokeRPC(rpcMethod as never, request.flowId)
          : await client.invokeRPC(rpcMethod as never, request.flowId, request.input);
      return { ok: true, result };
    }
  }
}

function findFlow(registry: Registry, flowType: string): Flow<any> {
  const flow = registry.flows.find((candidate) => candidate.getFlowType() === flowType);
  if (flow === undefined) {
    throw new Error(`unknown flow type ${flowType}`);
  }
  return flow;
}

function resolveRpcMethod(flow: object, rpcName: string): Function | undefined {
  let prototype: object | null = Object.getPrototypeOf(flow) as object | null;
  while (prototype !== null && prototype !== Object.prototype) {
    const descriptor = Object.getOwnPropertyDescriptor(prototype, rpcName);
    if (descriptor?.value instanceof Function) {
      return descriptor.value as Function;
    }
    prototype = Object.getPrototypeOf(prototype) as object | null;
  }
  return undefined;
}

function toErrorResponse(failure: unknown): SyncClientResponse {
  if (failure instanceof DexError) {
    return {
      ok: false,
      error: failure.detail,
      subStatus: failure.subStatus,
    };
  }
  return {
    ok: false,
    error: failure instanceof Error ? failure.message : String(failure),
  };
}

void run();
