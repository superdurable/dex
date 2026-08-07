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
import { MessageChannel, Worker, receiveMessageOnPort } from "node:worker_threads";

import type { StartFlowOptions } from "@superdurable/dex";

import { getWorkerTarget } from "../client-holder.js";
import { loadEnv } from "../config/env.js";

export interface SyncStartFlowRequest {
  readonly kind: "startFlow";
  readonly flowType: string;
  readonly flowId: string;
  readonly input: unknown;
  readonly options: StartFlowOptions;
}

export interface SyncWaitForFlowRequest {
  readonly kind: "waitForFlow";
  readonly flowId: string;
  readonly timeoutMs: number;
}

export interface SyncInvokeRpcRequest {
  readonly kind: "invokeRpc";
  readonly flowType: string;
  readonly flowId: string;
  readonly rpcName: string;
  readonly input?: unknown;
}

export type SyncClientRequest =
  | SyncStartFlowRequest
  | SyncWaitForFlowRequest
  | SyncInvokeRpcRequest;

export type SyncClientResponse =
  | { readonly ok: true; readonly result?: unknown }
  | { readonly ok: false; readonly error: string; readonly subStatus?: string };

export class SyncClientError extends Error {
  public constructor(
    message: string,
    public readonly subStatus?: string,
  ) {
    super(message);
    this.name = "SyncClientError";
  }
}

export function runClientSync(
  request: SyncClientRequest,
  workerUrl: URL = new URL("./dex-sync-worker.js", import.meta.url),
  extraWorkerData: Record<string, unknown> = {},
): SyncClientResponse {
  const serverAddress = loadEnv().serverAddress;
  const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-examples-sync-"));
  const workerTargetAddress = getWorkerTarget()?.address;
  const { port1, port2 } = new MessageChannel();
  const lock = new Int32Array(new SharedArrayBuffer(4));
  const worker = new Worker(workerUrl, {
    workerData: {
      serverAddress,
      request,
      port: port2,
      lock,
      cacheDirectory,
      workerTargetAddress,
      ...extraWorkerData,
    },
    transferList: [port2],
  });
  worker.unref();
  Atomics.wait(lock, 0, 0);
  const message = receiveMessageOnPort(port1) as SyncClientResponse | undefined;
  port1.close();
  void worker.terminate();
  if (message === undefined) {
    throw new Error("Dex sync worker returned no message");
  }
  return message;
}

export function startFlowSync(request: Omit<SyncStartFlowRequest, "kind">): void {
  const response = runClientSync({ kind: "startFlow", ...request });
  if (!response.ok) {
    throw new SyncClientError(response.error, response.subStatus);
  }
}

export function waitForFlowSync(
  flowId: string,
  timeoutMs: number,
): "completed" | "timeout" {
  const response = runClientSync({ kind: "waitForFlow", flowId, timeoutMs });
  if (response.ok) {
    return "completed";
  }
  if (response.subStatus === "longPollTimeout") {
    return "timeout";
  }
  throw new Error(response.error);
}

export function invokeRpcSync<T = unknown>(
  request: Omit<SyncInvokeRpcRequest, "kind">,
): T {
  const response = runClientSync({ kind: "invokeRpc", ...request });
  if (!response.ok) {
    throw new SyncClientError(response.error, response.subStatus);
  }
  return response.result as T;
}

export function notifySyncDone(lock: Int32Array): void {
  Atomics.store(lock, 0, 1);
  Atomics.notify(lock, 0);
}
