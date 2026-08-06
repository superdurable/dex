// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { laterPhase } from "./errors.js";

export interface BlobCacheConfig {
  readonly directory: string;
  readonly maxBytes: number;
  readonly frequencyCounters?: number;
}

export interface BlobCache {
  readonly config: BlobCacheConfig;
  get(blobId: string): Uint8Array | undefined;
  put(blobId: string, payload: Uint8Array): boolean;
  delete(blobId: string): void;
  deleteAll(): void;
  close(): void;
}

export function openBlobCache(config: BlobCacheConfig): BlobCache {
  if (config.directory.length === 0) {
    throw new TypeError("blob cache directory is required");
  }
  if (!Number.isSafeInteger(config.maxBytes) || config.maxBytes <= 0) {
    throw new RangeError("blob cache maxBytes must be a positive safe integer");
  }
  if (
    config.frequencyCounters !== undefined &&
    (!Number.isSafeInteger(config.frequencyCounters) || config.frequencyCounters < 0)
  ) {
    throw new RangeError("blob cache frequencyCounters must be a non-negative safe integer");
  }
  throw laterPhase("BlobCache bridge");
}
