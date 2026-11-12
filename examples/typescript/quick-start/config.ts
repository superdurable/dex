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

// Settings shared by the Worker, the start command, the service, and the tests.

import { mkdirSync } from "node:fs";

import { openBlobCache, type BlobCache } from "@superdurable/dex";

import { ShopApi } from "./shop-api.ts";

export const SERVER_ADDRESS = process.env.DEX_FLOW_SERVICE_ADDRESS ?? "127.0.0.1:8801";
export const HTTP_PORT = Number(process.env.QUICK_START_HTTP_PORT ?? "8080");
/** How long Ship waits, from QUICK_START_SHIP_DELAY_SECONDS; 30 seconds by default. */
export const SHIP_DELAY_MS = Number(process.env.QUICK_START_SHIP_DELAY_SECONDS ?? "30") * 1000;
const CHARGE_DELAY_SECONDS = Number(process.env.QUICK_START_CHARGE_DELAY_SECONDS ?? "0");

/** Returns the mock API that the Worker calls, with its keys in a local file. */
export function newShopApi(): ShopApi {
  return new ShopApi(".shop-api-keys", CHARGE_DELAY_SECONDS * 1000);
}

/** Opens a local cache for large Flow values, up to 1 GiB. */
export function openCache(directory: string): BlobCache {
  mkdirSync(directory, { recursive: true });
  return openBlobCache({ directory, maxBytes: 1 << 30 });
}
