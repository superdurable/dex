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

// Starts one OrderFlow. Pass an order ID, or let the command make one.

import { randomUUID } from "node:crypto";

import { Client, Registry } from "@superdurable/dex";

import { SERVER_ADDRESS, openCache } from "./config.ts";
import { OrderFlow } from "./order-flow.ts";
import { ShopApi } from "./shop-api.ts";

const orderId = process.argv[2] ?? `order-${randomUUID().slice(0, 8)}`;
const orderFlow = new OrderFlow(new ShopApi());
const registry = new Registry([orderFlow]);
const cache = openCache(".dex-blob-cache/client");
const client = new Client(registry, cache, { serverAddress: SERVER_ADDRESS });
try {
  await client.startFlow(orderFlow, orderId, orderId);
  console.log(`Started Flow ${orderId}. Ship runs in 30 seconds.`);
} finally {
  await client.close();
  cache.close();
}
