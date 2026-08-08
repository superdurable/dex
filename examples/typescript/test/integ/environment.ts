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

import { createServer } from "node:net";
import { randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { Client, Registry, Worker, openBlobCache, type Flow } from "@superdurable/dex";

import { HOUR_MS } from "../../src/config/env.js";
import { datasetDealFlow } from "../../src/workflow/datasetdeal/dataset-deal-flow.js";
import { engagementFlow } from "../../src/workflow/engagement/engagement-flow.js";
import { orchestrationFlow } from "../../src/workflow/microservices/orchestration-flow.js";
import { moneyTransferFlow } from "../../src/workflow/money/transfer/money-transfer-flow.js";
import { pollingFlow } from "../../src/workflow/polling/polling-flow.js";
import { subscriptionFlow } from "../../src/workflow/subscription/subscription-flow.js";

export interface IntegEnvironment {
  readonly client: Client;
  readonly datasetDealFlow: typeof datasetDealFlow;
  readonly moneyTransferFlow: typeof moneyTransferFlow;
  readonly engagementFlow: typeof engagementFlow;
  readonly orchestrationFlow: typeof orchestrationFlow;
  readonly pollingFlow: typeof pollingFlow;
  readonly subscriptionFlow: typeof subscriptionFlow;
  newFlowId(prefix: string): string;
  startOptions(): { timeoutMs: number };
  close(): Promise<void>;
}

let shared: IntegEnvironment | undefined;
let users = 0;

export async function acquireIntegEnvironment(): Promise<IntegEnvironment> {
  if (shared === undefined) {
    shared = await startIntegEnvironment();
  }
  users += 1;
  return shared;
}

export async function releaseIntegEnvironment(): Promise<void> {
  users -= 1;
  if (users === 0 && shared !== undefined) {
    await shared.close();
    shared = undefined;
  }
}

async function startIntegEnvironment(): Promise<IntegEnvironment> {
  const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
  const flows: readonly Flow<any>[] = [
    datasetDealFlow,
    moneyTransferFlow,
    engagementFlow,
    orchestrationFlow,
    pollingFlow,
    subscriptionFlow,
  ];
  const registry = new Registry(flows);
  const cacheDirectory = await mkdtemp(join(tmpdir(), "dex-typescript-examples-integ-"));
  const blobCache = openBlobCache({
    directory: cacheDirectory,
    maxBytes: 64 * 1024 * 1024,
  });
  const worker = new Worker(registry, blobCache, {
    bindAddress: `127.0.0.1:${await availablePort()}`,
    serverAddress,
  });
  await worker.start();
  const client = new Client(registry, blobCache, {
    serverAddress,
    workerTarget: worker.workerTarget,
  });
  return {
    client,
    datasetDealFlow,
    moneyTransferFlow,
    engagementFlow,
    orchestrationFlow,
    pollingFlow,
    subscriptionFlow,
    newFlowId(prefix: string) {
      return `${prefix}-${randomUUID()}`;
    },
    startOptions() {
      return { timeoutMs: HOUR_MS };
    },
    async close() {
      await Promise.race([
        (async () => {
          await client.close();
          await worker.close();
          blobCache.close();
          await rm(cacheDirectory, { recursive: true, force: true });
        })(),
        delay(3_000),
      ]);
    },
  };
}

export async function awaitCondition<T>(
  load: () => Promise<T>,
  predicate: (value: T) => boolean,
  timeoutMs: number,
  message: string,
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  let last: T | undefined;
  while (Date.now() < deadline) {
    last = await load();
    if (predicate(last)) {
      return last;
    }
    await delay(200);
  }
  throw new Error(`${message}; last=${JSON.stringify(last)}`);
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
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
