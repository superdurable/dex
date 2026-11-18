/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import java.util.Optional;

// The adaptive slot tests use no blob-backed values, so they need no native cache.
final class AdaptiveSlotTestBlobCache implements BlobCache {
    @Override
    public Optional<byte[]> get(final String blobId) {
        return Optional.empty();
    }

    @Override
    public boolean put(final String blobId, final byte[] payload) {
        return false;
    }

    @Override
    public void delete(final String blobId) {
    }

    @Override
    public void deleteAll() {
    }

    @Override
    public void close() {
    }
}
