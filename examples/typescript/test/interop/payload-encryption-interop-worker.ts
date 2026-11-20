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

// Serves the TypeScript payload interop Flow until SIGINT or SIGTERM.

import { once } from "node:events";

import { Registry, Worker, openBlobCache } from "@superdurable/dex";

import {
  TypeScriptPayloadInteropFlow,
  newFilePayloadEncryption,
  requiredEnv,
} from "./payload-encryption-interop-flow.js";

const cache = openBlobCache({
  directory: requiredEnv("DEX_PAYLOAD_INTEROP_CACHE_DIR"),
  maxBytes: 64 * 1024 * 1024,
});
const worker = new Worker(new Registry([new TypeScriptPayloadInteropFlow()]), cache, {
  serverAddress: requiredEnv("DEX_FLOW_SERVICE_ADDRESS"),
  pull: { maxConcurrentTasks: 16 },
  ...(await newFilePayloadEncryption(requiredEnv("DEX_PAYLOAD_INTEROP_KEY_FILE"))),
});
try {
  await worker.start();
  await worker.waitPullRegistered({ timeoutMs: 60_000 });
  console.log("payload interop worker ready: typescript");
  await Promise.race([once(process, "SIGINT"), once(process, "SIGTERM")]);
} finally {
  await worker.close();
  cache.close();
}
