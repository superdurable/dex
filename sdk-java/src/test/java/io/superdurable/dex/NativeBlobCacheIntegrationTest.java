/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class NativeBlobCacheIntegrationTest {
    private static final int READER_COUNT = 8;

    @TempDir
    Path cacheDirectory;

    @Test
    void roundTripsLargePayloadAcrossJni() {
        final byte[] payload = payload(8 * 1024 * 1024);
        try (BlobCache cache = openCache()) {
            assertTrue(cache.put("large-payload", payload));
            assertArrayEquals(
                    payload,
                    cache.get("large-payload").orElseThrow(AssertionError::new));
        }
    }

    @Test
    void coordinatesConcurrentReadsWithClose() throws Exception {
        final byte[] payload = payload(1024 * 1024);
        final BlobCache cache = openCache();
        assertTrue(cache.put("concurrent-payload", payload));

        final ExecutorService readers = Executors.newFixedThreadPool(READER_COUNT);
        final CountDownLatch ready = new CountDownLatch(READER_COUNT);
        final CountDownLatch start = new CountDownLatch(1);
        final List<Future<?>> results = new ArrayList<Future<?>>();
        try {
            for (int index = 0; index < READER_COUNT; index++) {
                results.add(readers.submit(() -> {
                    ready.countDown();
                    start.await();
                    for (int read = 0; read < 32; read++) {
                        try {
                            assertArrayEquals(
                                    payload,
                                    cache.get("concurrent-payload")
                                            .orElseThrow(AssertionError::new));
                        } catch (IllegalStateException closed) {
                            return null;
                        }
                    }
                    return null;
                }));
            }
            assertTrue(ready.await(10, TimeUnit.SECONDS));
            start.countDown();
            cache.close();
            for (Future<?> result : results) {
                result.get(30, TimeUnit.SECONDS);
            }
            assertThrows(
                    IllegalStateException.class,
                    () -> cache.get("concurrent-payload"));
            cache.close();
        } finally {
            start.countDown();
            readers.shutdownNow();
            assertTrue(readers.awaitTermination(10, TimeUnit.SECONDS));
            cache.close();
        }
    }

    private BlobCache openCache() {
        return BlobCache.open(new BlobCacheConfig(
                cacheDirectory.toString(),
                64L * 1024L * 1024L));
    }

    private static byte[] payload(final int size) {
        final byte[] payload = new byte[size];
        for (int index = 0; index < payload.length; index++) {
            payload[index] = (byte) (index * 31);
        }
        return payload;
    }
}
