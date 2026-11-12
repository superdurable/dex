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

// Runs the Worker that executes OrderFlow Steps. Stop it with Ctrl+C.

import { once } from "node:events";

import { Registry, Worker } from "@superdurable/dex";

import { SERVER_ADDRESS, newShopApi, openCache } from "./config.ts";
import { OrderFlow } from "./order-flow.ts";

const orderFlow = new OrderFlow(newShopApi());
const registry = new Registry([orderFlow]);
const cache = openCache(".dex-blob-cache/worker");
const worker = new Worker(registry, cache, { serverAddress: SERVER_ADDRESS });
const stopRequested = once(process, "SIGINT");
try {
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 30_000 });
  console.log(`Worker is ready (process ${process.pid}). Press Ctrl+C to stop it.`);
  await stopRequested;
  console.log("Stopping the Worker.");
} finally {
  await worker.close();
  cache.close();
}
