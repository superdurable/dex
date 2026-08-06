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

public final class Worker implements AutoCloseable {
    private final Registry registry;
    private final BlobCache blobCache;
    private final WorkerOptions options;

    public Worker(final Registry registry, final BlobCache blobCache) {
        this(registry, blobCache, WorkerOptions.newBuilder().build());
    }

    public Worker(
            final Registry registry,
            final BlobCache blobCache,
            final WorkerOptions options) {
        if (registry == null || blobCache == null || options == null) {
            throw new IllegalArgumentException("registry, blobCache, and options are required");
        }
        this.registry = registry;
        this.blobCache = blobCache;
        this.options = options;
    }

    Registry getRegistry() {
        return registry;
    }

    public void start() {
        throw new PhaseNotImplementedException("Worker runtime belongs to a later phase");
    }

    public void stop() {
        throw new PhaseNotImplementedException("Worker runtime belongs to a later phase");
    }

    @Override
    public void close() {
        stop();
    }
}
