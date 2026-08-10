/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import java.util.Optional;

/**
 * Caches immutable, server-addressed blob payloads on local disk.
 *
 * <p>{@link #open} uses the shared Rust DXBC implementation through JNI. Cache methods are
 * synchronous and safe to share through a {@link Client} and {@link Worker}. A missing or rejected
 * entry is a normal cache outcome; the server remains the source of truth. Close the cache after
 * every client and worker using it has stopped.
 *
 * <pre>{@code
 * try (BlobCache cache = BlobCache.open(
 *         new BlobCacheConfig("./dex-cache", 256L * 1024 * 1024))) {
 *     boolean cached = cache.put(blobId, payload);
 *     Optional<byte[]> value = cache.get(blobId);
 * }
 * }</pre>
 */
public interface BlobCache extends AutoCloseable {
    /**
     * Opens or recovers a disk-backed blob cache.
     *
     * @param config the cache directory, capacity, and admission configuration
     * @return the opened cache
     * @throws IllegalArgumentException if {@code config} is {@code null}
     * @throws RuntimeException if the native library cannot load or the cache cannot open
     */
    static BlobCache open(final BlobCacheConfig config) {
        if (config == null) {
            throw new IllegalArgumentException("config is required");
        }
        return new NativeBlobCache(config);
    }

    /**
     * Reads and validates one cached blob.
     *
     * @param blobId the server-minted blob identifier
     * @return a copy of the payload, or an empty value for a cache miss
     * @throws RuntimeException if cache storage cannot be read
     */
    Optional<byte[]> get(String blobId);

    /**
     * Offers one payload to the cache's admission policy.
     *
     * @param blobId the server-minted blob identifier
     * @param payload the immutable payload bytes to cache
     * @return {@code true} when admitted and stored; {@code false} when policy rejects the entry
     * @throws RuntimeException if cache storage cannot be written
     */
    boolean put(String blobId, byte[] payload);

    /**
     * Deletes one cached blob if present.
     *
     * @param blobId the server-minted blob identifier
     * @throws RuntimeException if cache storage cannot complete the deletion
     */
    void delete(String blobId);

    /**
     * Deletes all entries managed by this cache.
     *
     * @throws RuntimeException if cache storage cannot complete all deletions
     */
    void deleteAll();

    /**
     * Flushes pending cache work and releases native resources.
     *
     * <p>Do not call other methods after closing the cache.
     */
    @Override
    void close();
}
