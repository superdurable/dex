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

import { randomUUID } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { Client, Registry, Worker, openBlobCache, stringCodec } from "@superdurable/dex";

import { DELIVERED_ORDER_STATUS, orderStatusFlow } from "./order-status-flow.ts";

const serverAddress = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "localhost:8801";
const registry = new Registry([orderStatusFlow]);
const cacheDirectory = mkdtempSync(join(tmpdir(), "dex-decorator-free-rpc-"));
const blobCache = openBlobCache({ directory: cacheDirectory, maxBytes: 64 * 1_024 * 1_024 });
const worker = new Worker(registry, blobCache, { serverAddress });
const client = new Client(registry, blobCache, { serverAddress });

try {
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 30_000 });

  const orderId = `order-${randomUUID()}`;
  await client.startFlow(orderStatusFlow, orderId, orderId);
  await client.invokeRPC(orderStatusFlow.updateOrderStatus, orderId, "shipped");
  const shippedStatus = await client.invokeRPC(orderStatusFlow.getOrderStatus, orderId);
  console.log(`order ${orderId} status: ${shippedStatus}`);
  if (shippedStatus !== "shipped") {
    throw new Error(`expected status shipped, got ${shippedStatus}`);
  }

  await client.invokeRPC(orderStatusFlow.updateOrderStatus, orderId, DELIVERED_ORDER_STATUS);
  const result = await client.waitForFlow(orderId, 30_000);
  const completion = result.singleOutput(stringCodec);
  console.log(`order ${orderId} completed: ${completion}`);
  if (completion !== `${orderId}:${DELIVERED_ORDER_STATUS}`) {
    throw new Error(`unexpected completion ${completion}`);
  }
} finally {
  await client.close();
  await worker.close();
  blobCache.close();
  rmSync(cacheDirectory, { recursive: true, force: true });
}
