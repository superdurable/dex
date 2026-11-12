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

// Serves POST /orders and runs the Worker in the same process. Stop it with Ctrl+C.

import { once } from "node:events";

import { Client, Registry, Worker } from "@superdurable/dex";

import { HTTP_PORT, SERVER_ADDRESS, newShopApi, openCache } from "./config.ts";
import { OrderFlow } from "./order-flow.ts";
import { createOrderServer } from "./order-service.ts";

const orderFlow = new OrderFlow(newShopApi());
const registry = new Registry([orderFlow]);
const workerCache = openCache(".dex-blob-cache/worker");
const clientCache = openCache(".dex-blob-cache/client");
const worker = new Worker(registry, workerCache, { serverAddress: SERVER_ADDRESS });
const client = new Client(registry, clientCache, { serverAddress: SERVER_ADDRESS });
const server = createOrderServer(client, orderFlow);
const stopRequested = once(process, "SIGINT");
try {
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 30_000 });
  server.listen(HTTP_PORT, "127.0.0.1");
  await once(server, "listening");
  console.log(`Serving POST /orders on port ${HTTP_PORT} (process ${process.pid}).`);
  await stopRequested;
  console.log("Stopping the service.");
} finally {
  server.close();
  await client.close();
  await worker.close();
  clientCache.close();
  workerCache.close();
}
