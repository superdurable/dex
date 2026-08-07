// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  Client,
  Registry,
  Worker,
  openBlobCache,
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
  const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-typescript-integration-cache-"));
  const blobCache = openBlobCache({
    directory: cacheDirectory,
    maxBytes: 64 * 1_024 * 1_024,
  });
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
      rmSync(cacheDirectory, { recursive: true, force: true });
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
