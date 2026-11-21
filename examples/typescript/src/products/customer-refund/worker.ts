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

/** Runs the Worker for CustomerRefundFlow. Stop it with Ctrl+C. */

import { Registry, Worker } from "@superdurable/dex";

import {
  newPayloadCodec,
  newRefundFlow,
  openRefundBlobCache,
  readRefundEnvironment,
  refundWorkerOptions,
} from "./refund-settings.js";

const environment = readRefundEnvironment();
const payloadCodec = await newPayloadCodec(environment.payloadKeyFile);
const registry = new Registry([newRefundFlow(environment.billingMode)]);
const cache = openRefundBlobCache(".dex-blob-cache/customer-refund-worker");
const options = refundWorkerOptions(environment.serverAddress, payloadCodec);
const worker = new Worker(registry, cache, options);
try {
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 30_000 });
  const encryption = payloadCodec === undefined ? "off" : "on";
  console.log(
    `Worker ${options.pull?.appWorkerID} is ready ` +
      `(billing mode ${environment.billingMode}, encryption ${encryption}). Press Ctrl+C to stop it.`,
  );
  await new Promise<void>((resolve) => {
    process.once("SIGINT", () => resolve());
    process.once("SIGTERM", () => resolve());
  });
  console.log("Stopping the Worker.");
} finally {
  await worker.close();
  cache.close();
}
