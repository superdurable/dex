// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { createServer } from "node:net";

import {
  Client,
  Registry,
  Worker,
  type BlobCache,
  type Flow,
} from "../../src/index.js";

export interface TestEnvironment {
  readonly client: Client;
  readonly worker: Worker;
  close(): Promise<void>;
}

export async function startEnvironment(...flows: readonly Flow<any>[]): Promise<TestEnvironment> {
  const serverAddress = process.env.DEX_SERVER_ADDRESS ?? "127.0.0.1:8801";
  const workerAddress = `127.0.0.1:${await availablePort()}`;
  const registry = new Registry(flows);
  const blobCache = new MemoryBlobCache();
  const worker = new Worker(registry, blobCache, {
    bindAddress: workerAddress,
    serverAddress,
  });
  await worker.start();
  const client = new Client(registry, blobCache, {
    serverAddress,
    workerTarget: worker.workerTarget,
  });
  return {
    client,
    worker,
    async close() {
      await client.close();
      await worker.close();
      blobCache.close();
    },
  };
}

export async function withEnvironment(
  flows: readonly Flow<any>[],
  run: (environment: TestEnvironment) => Promise<void>,
): Promise<void> {
  const environment = await startEnvironment(...flows);
  try {
    await run(environment);
  } finally {
    await environment.close();
  }
}

export function flowId(prefix: string): string {
  return `${prefix}-${randomUUID()}`;
}

export async function expectError<Failure extends Error>(
  operation: Promise<unknown>,
  errorType: abstract new (...arguments_: any[]) => Failure,
): Promise<Failure> {
  try {
    await operation;
  } catch (failure) {
    assert.ok(failure instanceof errorType);
    return failure;
  }
  assert.fail(`expected ${errorType.name}`);
}

class MemoryBlobCache implements BlobCache {
  public readonly config = { directory: "memory", maxBytes: 64 * 1_024 * 1_024 };
  private readonly values = new Map<string, Uint8Array>();

  public get(blobId: string): Uint8Array | undefined {
    return this.values.get(blobId)?.slice();
  }

  public put(blobId: string, payload: Uint8Array): boolean {
    this.values.set(blobId, payload.slice());
    return true;
  }

  public delete(blobId: string): void {
    this.values.delete(blobId);
  }

  public deleteAll(): void {
    this.values.clear();
  }

  public close(): void {
    this.values.clear();
  }
}

function availablePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        server.close();
        reject(new Error("failed to allocate a Worker port"));
        return;
      }
      server.close((error) => {
        if (error !== undefined) {
          reject(error);
          return;
        }
        resolve(address.port);
      });
    });
  });
}
