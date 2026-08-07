// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

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

interface NativeBlobCacheBinding {
  get(blobId: string): Buffer | null;
  put(blobId: string, payload: Buffer): boolean;
  delete(blobId: string): void;
  deleteAll(): void;
  close(): void;
}

interface NativeBlobCacheConstructor {
  new (
    directory: string,
    maxBytes: number,
    frequencyCounters: number,
  ): NativeBlobCacheBinding;
}

interface NativeModule {
  NativeBlobCache: NativeBlobCacheConstructor;
}

const require = createRequire(import.meta.url);
let nativeModule: NativeModule | undefined;

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
  return new RustBlobCache(config, loadNativeModule());
}

class RustBlobCache implements BlobCache {
  public readonly config: BlobCacheConfig;
  private readonly native: NativeBlobCacheBinding;

  public constructor(config: BlobCacheConfig, binding: NativeModule) {
    this.config = config;
    this.native = new binding.NativeBlobCache(
      config.directory,
      config.maxBytes,
      config.frequencyCounters ?? 0,
    );
  }

  public get(blobId: string): Uint8Array | undefined {
    const payload = this.native.get(blobId);
    return payload === null ? undefined : new Uint8Array(payload);
  }

  public put(blobId: string, payload: Uint8Array): boolean {
    return this.native.put(blobId, Buffer.from(payload));
  }

  public delete(blobId: string): void {
    this.native.delete(blobId);
  }

  public deleteAll(): void {
    this.native.deleteAll();
  }

  public close(): void {
    this.native.close();
  }
}

function loadNativeModule(): NativeModule {
  if (nativeModule !== undefined) {
    return nativeModule;
  }
  const candidates = nativeCandidates();
  const failures: string[] = [];
  for (const candidate of candidates) {
    try {
      nativeModule = require(candidate) as NativeModule;
      return nativeModule;
    } catch (failure) {
      failures.push(`${candidate}: ${failure instanceof Error ? failure.message : String(failure)}`);
    }
  }
  throw new Error(
    `cannot load the Dex BlobCache native module for ${process.platform}/${process.arch}; ` +
      `set DEX_BLOB_CACHE_NATIVE to an absolute .node path to override packaged natives. ` +
      `Tried:\n${failures.join("\n")}`,
  );
}

function nativeCandidates(): string[] {
  const override = process.env.DEX_BLOB_CACHE_NATIVE;
  if (override !== undefined && override.length > 0) {
    return [override];
  }
  const packageRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
  const packaged = join(
    packageRoot,
    "native",
    `${operatingSystem()}-${architecture()}`,
    "dex_blob_cache_node.node",
  );
  return [packaged];
}

function operatingSystem(): string {
  switch (process.platform) {
    case "darwin":
      return "macos";
    case "linux":
      return "linux";
    case "win32":
      return "windows";
    default:
      throw new Error(`unsupported operating system: ${process.platform}`);
  }
}

function architecture(): string {
  switch (process.arch) {
    case "x64":
      return "x86_64";
    case "arm64":
      return "aarch64";
    default:
      throw new Error(`unsupported architecture: ${process.arch}`);
  }
}
