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
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import type { AddressInfo } from "node:net";
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
import { createOrderServer } from "../order-service.ts";
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
    assert.deepEqual(callsFor(orderId, firstShop.charged), [orderId]);
    assert.deepEqual(callsFor(orderId, firstShop.shipped), []);

    const result = await withRunningWorker(
      new OrderFlow(secondShop),
      join(cacheRoot, "worker-2"),
      async () => {
        await skipShipDelay(client, orderId);
        return client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
      },
    );

    assert.equal(result.status, "completed");
    assert.deepEqual(callsFor(orderId, secondShop.charged), []);
    assert.deepEqual(callsFor(orderId, secondShop.shipped), [orderId]);
  } finally {
    await client.close();
    clientCache.close();
    await rm(cacheRoot, { recursive: true, force: true });
  }
});

test("the mock API drops a repeated key across processes", async () => {
  const keysDir = await mkdtemp(join(tmpdir(), "dex-quick-start-keys-"));
  try {
    const keysPath = join(keysDir, "keys");
    const firstProcess = new ShopApi(keysPath);
    const secondProcess = new ShopApi(keysPath);
    await firstProcess.charge("order-1", "charge-order-1");
    await secondProcess.charge("order-1", "charge-order-1");
    assert.deepEqual(firstProcess.charged, ["order-1"]);
    assert.deepEqual(secondProcess.charged, []);
    assert.deepEqual(secondProcess.repeatedKeys, ["charge-order-1"]);
  } finally {
    await rm(keysDir, { recursive: true, force: true });
  }
});

test("the service starts one Flow for a repeated request", async (t) => {
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
    const shop = new ShopApi();
    await withRunningWorker(new OrderFlow(shop), join(cacheRoot, "worker"), async () => {
      const server = createOrderServer(client, clientFlow);
      server.listen(0, "127.0.0.1");
      await once(server, "listening");
      try {
        const { port } = server.address() as AddressInfo;
        for (let attempt = 0; attempt < 2; attempt++) {
          const response = await fetch(`http://127.0.0.1:${port}/orders`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ orderId }),
          });
          assert.equal(response.status, 200);
          assert.deepEqual(await response.json(), { flowId: orderId, charged: true });
        }
      } finally {
        server.close();
      }
      await skipShipDelay(client, orderId);
      await client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
    });
    assert.deepEqual(callsFor(orderId, shop.charged), [orderId]);
  } finally {
    await client.close();
    clientCache.close();
    await rm(cacheRoot, { recursive: true, force: true });
  }
});

/** Keeps this test's order; a Flow left open by an earlier run can also reach a Worker. */
function callsFor(orderId: string, calls: readonly string[]): string[] {
  return calls.filter((call) => call === orderId);
}

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

/** Skips the 60-second Timer once Ship waits on it. */
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
