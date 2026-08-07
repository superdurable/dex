// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { BlobCache } from "./blob-cache.js";
import { laterPhase } from "./errors.js";
import type { Registry } from "./flow.js";
import type { WorkerOptions } from "./options.js";

export class Worker {
  public constructor(
    public readonly registry: Registry,
    public readonly blobCache: BlobCache,
    public readonly options: WorkerOptions = {},
  ) {}

  public async run(): Promise<void> {
    throw laterPhase("Worker runtime");
  }

  public async close(): Promise<void> {
    throw laterPhase("Worker runtime");
  }
}
