// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** Configures the local persistent cache for large Dex values. */
export interface BlobCacheConfig {
  /**
   * Writable directory used for cache payloads and metadata.
   * Only one open cache may use this directory at a time. See {@link openBlobCache}.
   */
  readonly directory: string;
  /** Positive maximum on-disk payload size in bytes. */
  readonly maxBytes: number;
  /** Admission-policy counter count; zero or `undefined` uses the native default. */
  readonly frequencyCounters?: number;
}

/** Provides concurrent, bounded storage for content-addressed Dex blobs. */
export interface BlobCache {
  /** Effective configuration used by this cache. */
  readonly config: BlobCacheConfig;
  /**
   * Reads a cached payload without contacting Dex.
   * @param blobId - Opaque server-assigned blob identifier.
   * @returns A copied payload, or `undefined` when absent.
   */
  get(blobId: string): Uint8Array | undefined;
  /**
   * Offers a payload to the bounded cache.
   * When the Client or Worker hydrates a large value and this call throws, the SDK logs the error
   * with `console.warn` and uses the value it loaded from Dex. The Step, RPC, or Client call
   * continues.
   * Changed in 1.45.17: a thrown error no longer fails the hydration.
   * Changed in 1.45.17: when the blob's file is already on disk with the same bytes, such as a file
   * that another open cache on the same directory wrote, `put` keeps that file as this cache's entry
   * and returns `true`. It counts the file against `maxBytes` like a new write.
   * @param blobId - Opaque server-assigned blob identifier.
   * @param payload - Bytes copied into native storage.
   * @returns `true` when admitted, including when the same bytes are already on disk, or `false`
   * when rejected by policy.
   * @throws {@link Error} when the payload cannot be stored, such as when the file for this blob ID
   * already holds different bytes ("blob ID content mismatch").
   */
  put(blobId: string, payload: Uint8Array): boolean;
  /**
   * Deletes one cached payload when present.
   * @param blobId - Opaque identifier to remove.
   */
  delete(blobId: string): void;
  /** Deletes every payload while keeping the cache open. */
  deleteAll(): void;
  /** Flushes metadata and releases native resources; repeated calls are safe. */
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

/**
 * Opens or creates a native BlobCache owned by the caller.
 *
 * Open each cache directory once, and share the returned cache between the Worker and the Client
 * of the process. Only one open cache may use a directory at a time, whether the two caches are in
 * one process or in two. Separate processes, such as two replicas of one Worker, need separate
 * directories. A restarted process can reuse its directory and the blobs already in it.
 *
 * The cache takes no lock on its directory, so a second cache can still open it. The two caches
 * then work against each other. Neither sees a blob that the other stores after it opened, so it
 * loads that blob from Dex again. Since 1.45.17, its write of that blob accepts the file already on
 * disk when the bytes are identical. Each cache evicts and deletes
 * files that the other still lists. Opening a cache also deletes the other cache's unfinished
 * writes. Each cache counts `maxBytes` on its own, so the directory can grow past that limit.
 *
 * @example
 * ```ts
 * // Only one running process may use .dex-cache. Give each Worker replica its own directory.
 * const cache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 ** 2 });
 * try { cache.put("blob-1", new Uint8Array([1, 2])); } finally { cache.close(); }
 * ```
 * @param config - Directory, positive byte capacity, and optional counter count.
 * @returns An open cache that must be closed at application shutdown.
 * @throws {@link TypeError} when the directory is empty.
 * @throws {@link RangeError} when a numeric setting is invalid.
 */
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
