/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.consumer;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Arrays;
import java.util.Comparator;
import java.util.stream.Stream;

public final class PublicationSmoke {
    private PublicationSmoke() {
    }

    public static void main(final String[] arguments) throws IOException {
        final Path directory = Files.createTempDirectory("dex-java-publication-");
        try {
            final byte[] payload = "published-native-cache".getBytes(StandardCharsets.UTF_8);
            try (BlobCache cache = BlobCache.open(
                    new BlobCacheConfig(directory.toString(), 1024 * 1024))) {
                if (!cache.put("publication-smoke", payload)) {
                    throw new IllegalStateException("published BlobCache rejected the smoke value");
                }
                final byte[] cached = cache.get("publication-smoke").orElseThrow(
                        () -> new IllegalStateException("published BlobCache missed the smoke value"));
                if (!Arrays.equals(payload, cached)) {
                    throw new IllegalStateException("published BlobCache changed the smoke value");
                }
            }
        } finally {
            deleteRecursively(directory);
        }
    }

    private static void deleteRecursively(final Path directory) throws IOException {
        try (Stream<Path> paths = Files.walk(directory)) {
            try {
                paths.sorted(Comparator.reverseOrder()).forEach(path -> {
                    try {
                        Files.deleteIfExists(path);
                    } catch (IOException failure) {
                        throw new UncheckedIOException(failure);
                    }
                });
            } catch (UncheckedIOException failure) {
                throw failure.getCause();
            }
        }
    }
}
