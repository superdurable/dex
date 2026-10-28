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

import {
  Worker,
  type BlobCache,
  type PullWorkerOptions,
  type Registry,
} from "@superdurable/dex";

const PULL_REGISTRATION_TIMEOUT_MS = 30_000;

/** Returns pull options sized for every sample Flow sharing one Worker process. */
export function newExamplePullWorkerOptions(): PullWorkerOptions {
  return { maxConcurrentTasks: 64 };
}

/** Starts a pull Worker whose task streams Dex Server registered. */
export async function startExampleWorker(
  registry: Registry,
  blobCache: BlobCache,
  serverAddress: string,
): Promise<Worker> {
  const worker = new Worker(registry, blobCache, {
    serverAddress,
    pull: newExamplePullWorkerOptions(),
  });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: PULL_REGISTRATION_TIMEOUT_MS });
  } catch (failure) {
    try {
      await worker.close();
    } catch (cleanupFailure) {
      throw new AggregateError([failure, cleanupFailure], "pull Worker startup and cleanup failed");
    }
    throw failure;
  }
  return worker;
}
