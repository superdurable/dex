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

public final class BlobCacheConfig {
    private static final long DEFAULT_FREQUENCY_COUNTERS = 10_000L;

    private final String directory;
    private final long maxBytes;
    private final long frequencyCounters;

    public BlobCacheConfig(final String directory, final long maxBytes) {
        this(directory, maxBytes, DEFAULT_FREQUENCY_COUNTERS);
    }

    public BlobCacheConfig(
            final String directory,
            final long maxBytes,
            final long frequencyCounters) {
        if (directory == null || directory.isEmpty()) {
            throw new IllegalArgumentException("blob cache directory is required");
        }
        if (maxBytes <= 0) {
            throw new IllegalArgumentException("blob cache maxBytes must be positive");
        }
        if (frequencyCounters < 0) {
            throw new IllegalArgumentException(
                    "blob cache frequencyCounters must not be negative");
        }
        this.directory = directory;
        this.maxBytes = maxBytes;
        this.frequencyCounters = frequencyCounters == 0
                ? DEFAULT_FREQUENCY_COUNTERS
                : frequencyCounters;
    }

    String getDirectory() {
        return directory;
    }

    long getMaxBytes() {
        return maxBytes;
    }

    long getFrequencyCounters() {
        return frequencyCounters;
    }
}
