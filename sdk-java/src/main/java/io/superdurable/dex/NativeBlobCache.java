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

final class NativeBlobCache implements BlobCache {
    private long handle;

    NativeBlobCache(final BlobCacheConfig config) {
        this.handle = NativeCore.cacheOpen(
                config.getDirectory(),
                config.getMaxBytes(),
                config.getFrequencyCounters());
    }

    @Override
    public synchronized Optional<byte[]> get(final String blobId) {
        return Optional.ofNullable(NativeCore.cacheGet(requireOpen(), blobId));
    }

    @Override
    public synchronized boolean put(final String blobId, final byte[] payload) {
        return NativeCore.cachePut(requireOpen(), blobId, payload);
    }

    @Override
    public synchronized void delete(final String blobId) {
        NativeCore.cacheDelete(requireOpen(), blobId);
    }

    @Override
    public synchronized void deleteAll() {
        NativeCore.cacheDeleteAll(requireOpen());
    }

    @Override
    public synchronized void close() {
        if (handle == 0) {
            return;
        }
        NativeCore.cacheClose(handle);
        handle = 0;
    }

    private long requireOpen() {
        if (handle == 0) {
            throw new IllegalStateException("BlobCache is closed");
        }
        return handle;
    }
}
