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
import java.util.concurrent.locks.Lock;
import java.util.concurrent.locks.ReentrantReadWriteLock;

final class NativeBlobCache implements BlobCache {
    private final ReentrantReadWriteLock lifecycle = new ReentrantReadWriteLock();
    private long handle;

    NativeBlobCache(final BlobCacheConfig config) {
        this.handle = NativeBlobCacheBridge.cacheOpen(
                config.getDirectory(),
                config.getMaxBytes(),
                config.getFrequencyCounters());
    }

    @Override
    public Optional<byte[]> get(final String blobId) {
        final Lock lock = lifecycle.readLock();
        lock.lock();
        try {
            return Optional.ofNullable(
                    NativeBlobCacheBridge.cacheGet(requireOpen(), blobId));
        } finally {
            lock.unlock();
        }
    }

    @Override
    public boolean put(final String blobId, final byte[] payload) {
        final Lock lock = lifecycle.readLock();
        lock.lock();
        try {
            return NativeBlobCacheBridge.cachePut(requireOpen(), blobId, payload);
        } finally {
            lock.unlock();
        }
    }

    @Override
    public void delete(final String blobId) {
        final Lock lock = lifecycle.readLock();
        lock.lock();
        try {
            NativeBlobCacheBridge.cacheDelete(requireOpen(), blobId);
        } finally {
            lock.unlock();
        }
    }

    @Override
    public void deleteAll() {
        final Lock lock = lifecycle.readLock();
        lock.lock();
        try {
            NativeBlobCacheBridge.cacheDeleteAll(requireOpen());
        } finally {
            lock.unlock();
        }
    }

    @Override
    public void close() {
        final Lock lock = lifecycle.writeLock();
        lock.lock();
        try {
            if (handle == 0) {
                return;
            }
            final long closingHandle = handle;
            handle = 0;
            NativeBlobCacheBridge.cacheClose(closingHandle);
        } finally {
            lock.unlock();
        }
    }

    private long requireOpen() {
        if (handle == 0) {
            throw new IllegalStateException("BlobCache is closed");
        }
        return handle;
    }
}
