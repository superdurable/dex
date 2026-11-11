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

// Runs the Quick Start against a real Dex server.
//
// Point DEX_FLOW_SERVICE_ADDRESS at the server (default 127.0.0.1:8801). Without
// a server the test fails, unless DEX_EXAMPLES_ALLOW_SKIP=1 asks it to skip.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { test } from "node:test";

import {
  Client,
  DexServiceError,
  FlowNotFoundError,
  Registry,
  StepExecutionId,
  TimerId,
  Worker,
} from "@superdurable/dex";

import { SERVER_ADDRESS, openCache } from "../config.ts";
import { OrderFlow, SHIP_DELAY_TIMER } from "../order-flow.ts";
import { ShopApi } from "../shop-api.ts";

const WAIT_TIMEOUT_MS = 60_000;

test("a restarted Worker ships without charging again", async (t) => {
  const cacheRoot = await mkdtemp(join(tmpdir(), "dex-quick-start-"));
  const clientFlow = new OrderFlow(new ShopApi());
  const clientCache = openCache(join(cacheRoot, "client"));
  const client = new Client(new Registry([clientFlow]), clientCache, {
    serverAddress: SERVER_ADDRESS,
  });
  try {
    if (!(await isServerReady(client))) {
      const message = `no Dex server at ${SERVER_ADDRESS}; set DEX_FLOW_SERVICE_ADDRESS`;
      if (process.env.DEX_EXAMPLES_ALLOW_SKIP === "1") {
        t.skip(message);
        return;
      }
      assert.fail(message);
    }

    const orderId = `order-${randomUUID()}`;
    const firstShop = new ShopApi();
    const secondShop = new ShopApi();

    await withRunningWorker(new OrderFlow(firstShop), join(cacheRoot, "worker-1"), async () => {
      await client.startFlow(clientFlow, orderId, orderId);
      await client.waitForStepCompletion(orderId, StepExecutionId.of("Charge"), {
        requestTimeoutMs: WAIT_TIMEOUT_MS,
      });
    });
    assert.deepEqual(firstShop.charged, [orderId]);
    assert.deepEqual(firstShop.shipped, []);

    const result = await withRunningWorker(
      new OrderFlow(secondShop),
      join(cacheRoot, "worker-2"),
      async () => {
        await skipShipDelay(client, orderId);
        return client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
      },
    );

    assert.equal(result.status, "completed");
    assert.deepEqual(secondShop.charged, []);
    assert.deepEqual(secondShop.shipped, [orderId]);
  } finally {
    await client.close();
    clientCache.close();
    await rm(cacheRoot, { recursive: true, force: true });
  }
});

async function withRunningWorker<T>(
  orderFlow: OrderFlow,
  cacheDirectory: string,
  body: () => Promise<T>,
): Promise<T> {
  const cache = openCache(cacheDirectory);
  const worker = new Worker(new Registry([orderFlow]), cache, { serverAddress: SERVER_ADDRESS });
  try {
    await worker.start();
    await worker.waitPullRegistered({ timeoutMs: 30_000 });
    return await body();
  } finally {
    await worker.close();
    cache.close();
  }
}

/** Reports whether the server answers, by describing a Flow that does not exist. */
async function isServerReady(client: Client): Promise<boolean> {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    try {
      await client.describeFlow(`missing-${randomUUID()}`);
      return true;
    } catch (error) {
      if (error instanceof FlowNotFoundError) {
        return true;
      }
      if (!(error instanceof DexServiceError)) {
        throw error;
      }
    }
    await sleep(250);
  }
  return false;
}

/** Skips the 30-second Timer once Ship waits on it. */
async function skipShipDelay(client: Client, orderId: string): Promise<void> {
  const deadline = Date.now() + WAIT_TIMEOUT_MS;
  for (;;) {
    try {
      await client.skipTimer(
        orderId,
        StepExecutionId.of("Ship"),
        TimerId.byConditionId(SHIP_DELAY_TIMER),
      );
      return;
    } catch (error) {
      if (!(error instanceof DexServiceError) || Date.now() >= deadline) {
        throw error;
      }
      await sleep(250);
    }
  }
}
