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

import { type MessagePort, workerData } from "node:worker_threads";

import { Client, DexError, ErrorSubStatus, Registry, openBlobCache } from "@superdurable/dex";

import { notifySyncDone } from "../../patterns/client-sync.js";
import { employerOptInFlow } from "./employer-opt-in-flow.js";
import { employerOptIn } from "./workflow-ids.js";

interface WorkerData {
  readonly employerId: string;
  readonly serverAddress: string;
  readonly port: MessagePort;
  readonly lock: Int32Array;
  readonly cacheDirectory: string;
  readonly workerTargetAddress?: string;
}

async function run(): Promise<void> {
  const {
    employerId,
    serverAddress,
    port,
    lock,
    cacheDirectory,
    workerTargetAddress,
  } = workerData as WorkerData;
  const blobCache = openBlobCache({
    directory: cacheDirectory,
    maxBytes: 1 << 20,
  });
  const registry = new Registry([employerOptInFlow]);
  const client = new Client(registry, blobCache, {
    serverAddress,
    ...(workerTargetAddress !== undefined && workerTargetAddress.length > 0
      ? { workerTarget: { address: workerTargetAddress } }
      : {}),
  });
  try {
    const result = await client.invokeRPC(
      employerOptInFlow.isOptedIn,
      employerOptIn(employerId),
    );
    port.postMessage({ ok: true, result: Boolean(result) });
  } catch (failure) {
    if (failure instanceof DexError && failure.subStatus === ErrorSubStatus.FLOW_NOT_EXISTS) {
      port.postMessage({ ok: true, result: false });
    } else {
      port.postMessage({
        ok: false,
        error: failure instanceof Error ? failure.message : String(failure),
      });
    }
  } finally {
    await client.close();
    blobCache.close();
    port.close();
    notifySyncDone(lock);
  }
}

void run();
