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

/**
 * Configures the disk-backed {@link BlobCache}.
 *
 * <p>The byte capacity limits admitted cache contents; it is not a durable-storage quota or a
 * guarantee that every offered blob will be retained.
 *
 * <p>{@code frequencyCounters} controls the requested number of 4-bit access counters in the
 * in-memory TinyLFU sketch that approximately tracks how often blob IDs are accessed. The cache
 * uses these estimates to prefer frequently reused blobs over one-use blobs when admitting or
 * evicting data. It is not the maximum number of cached blobs, an exact access-history length, or
 * part of the disk-capacity calculation.
 *
 * <p>A larger value uses more process memory but reduces collisions between unrelated blob IDs,
 * making admission and eviction decisions more accurate. A smaller value saves memory, but scan
 * traffic can make cold blobs appear hotter than they are and reduce the expected cache hit ratio.
 * The value does not affect cache correctness, file integrity, or {@code maxBytes} enforcement.
 * Access history is not preserved across cache reopenings.
 *
 * <p>Use approximately ten counters per blob that the cache is expected to hold when full. For
 * example, if {@code maxBytes} usually holds about 1,000 blobs, 10,000 counters is a reasonable
 * starting point. The policy uses approximately three bytes of memory per requested counter before
 * internal rounding, so requesting 10,000 counters represents about 30 KB before rounding; actual
 * memory use can be higher. Increase the value for a much larger expected item count or a workload
 * with substantial one-use scan traffic. Decrease it only when policy memory matters more than
 * admission accuracy. Passing zero uses the default of 10,000.
 *
 * <pre>{@code
 * BlobCacheConfig config = new BlobCacheConfig(
 *         "./dex-cache",
 *         256L * 1024 * 1024,
 *         10_000L);
 * }</pre>
 */
public final class BlobCacheConfig {
    private static final long DEFAULT_FREQUENCY_COUNTERS = 10_000L;

    private final String directory;
    private final long maxBytes;
    private final long frequencyCounters;

    /**
     * Creates a configuration with 10,000 frequency counters.
     *
     * @param directory the nonempty directory used for cache files
     * @param maxBytes the positive maximum admitted payload size in bytes
     * @throws IllegalArgumentException if the directory is empty or the capacity is not positive
     */
    public BlobCacheConfig(final String directory, final long maxBytes) {
        this(directory, maxBytes, DEFAULT_FREQUENCY_COUNTERS);
    }

    /**
     * Creates a configuration with an explicit admission-sketch size.
     *
     * @param directory the nonempty directory used for cache files
     * @param maxBytes the positive maximum admitted payload size in bytes
     * @param frequencyCounters the nonnegative TinyLFU sketch size described above; zero uses the
     *     default of 10,000
     * @throws IllegalArgumentException if the directory is empty, the capacity is not positive, or
     *     {@code frequencyCounters} is negative
     */
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
