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

import org.openjdk.jmh.annotations.Benchmark;
import org.openjdk.jmh.annotations.BenchmarkMode;
import org.openjdk.jmh.annotations.Fork;
import org.openjdk.jmh.annotations.Level;
import org.openjdk.jmh.annotations.Measurement;
import org.openjdk.jmh.annotations.Mode;
import org.openjdk.jmh.annotations.OutputTimeUnit;
import org.openjdk.jmh.annotations.Param;
import org.openjdk.jmh.annotations.Scope;
import org.openjdk.jmh.annotations.Setup;
import org.openjdk.jmh.annotations.State;
import org.openjdk.jmh.annotations.TearDown;
import org.openjdk.jmh.annotations.Warmup;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.Comparator;
import java.util.concurrent.TimeUnit;
import java.util.stream.Stream;

@BenchmarkMode(Mode.Throughput)
@OutputTimeUnit(TimeUnit.SECONDS)
@Fork(1)
@Warmup(iterations = 3)
@Measurement(iterations = 5)
@State(Scope.Benchmark)
public class NativeBlobCacheBenchmark {
    @Param({"1024", "65536", "1048576", "8388608"})
    private int payloadSize;

    private BlobCache cache;
    private Path directory;
    private byte[] payload;

    @Setup(Level.Trial)
    public void open() throws Exception {
        directory = Files.createTempDirectory("dex-jni-blob-cache-benchmark-");
        cache = BlobCache.open(new BlobCacheConfig(
                directory.toString(),
                64L * 1024L * 1024L));
        payload = new byte[payloadSize];
        for (int index = 0; index < payload.length; index++) {
            payload[index] = (byte) (index * 31);
        }
        if (!cache.put("benchmark-hit", payload)) {
            throw new IllegalStateException("benchmark payload was not cached");
        }
    }

    @TearDown(Level.Trial)
    public void close() throws Exception {
        cache.close();
        try (Stream<Path> paths = Files.walk(directory)) {
            paths.sorted(Comparator.reverseOrder())
                    .forEach(NativeBlobCacheBenchmark::delete);
        }
    }

    @Benchmark
    public byte[] getHit() {
        return cache.get("benchmark-hit").orElseThrow(AssertionError::new);
    }

    @Benchmark
    public boolean reusePut() {
        return cache.put("benchmark-hit", payload);
    }

    private static void delete(final Path path) {
        try {
            Files.delete(path);
        } catch (IOException failure) {
            throw new UncheckedIOException(failure);
        }
    }
}
