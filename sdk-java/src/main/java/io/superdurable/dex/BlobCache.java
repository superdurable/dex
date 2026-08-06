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

public interface BlobCache extends AutoCloseable {
    static BlobCache open(final BlobCacheConfig config) {
        if (config == null) {
            throw new IllegalArgumentException("config is required");
        }
        throw new PhaseNotImplementedException("BlobCache bridge belongs to a later phase");
    }

    Optional<byte[]> get(String blobId);

    boolean put(String blobId, byte[] payload);

    void delete(String blobId);

    void deleteAll();

    @Override
    void close();
}
