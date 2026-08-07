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

import type { Client, WorkerTarget } from "@superdurable/dex";

let holder: Client | undefined;
let syncWorkerTargetHolder: WorkerTarget | undefined;

export function setClient(client: Client, syncWorkerTarget: WorkerTarget): void {
  holder = client;
  syncWorkerTargetHolder = syncWorkerTarget;
}

export function getClient(): Client {
  if (holder === undefined) {
    throw new Error("Dex client is not initialized; call setClient first");
  }
  return holder;
}

/** Worker used by sync Client calls from Steps (avoids Atomics.wait deadlock). */
export function getWorkerTarget(): WorkerTarget | undefined {
  return syncWorkerTargetHolder;
}
