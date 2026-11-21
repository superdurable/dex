/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.products.customerrefund;

import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.SerializationFeature;

import java.io.IOException;
import java.io.UncheckedIOException;
import java.nio.ByteBuffer;
import java.nio.channels.FileChannel;
import java.nio.channels.FileLock;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;
import java.util.function.Function;

/** A JSON record list changed under a file lock and replaced by atomic rename. */
final class LockedJsonLedger<T> {
    private static final ObjectMapper JSON = new ObjectMapper().enable(SerializationFeature.INDENT_OUTPUT);
    // A JVM may hold only one FileLock per file, so threads queue on this monitor first.
    private static final ConcurrentHashMap<Path, Object> MONITORS = new ConcurrentHashMap<>();

    private final Path path;
    private final Path lockPath;
    private final JavaType listType;

    /** Records to write, or null to keep the file, and the change's result. */
    record Change<R>(List<?> recordsToWrite, R result) {
    }

    LockedJsonLedger(final Path path, final Class<T> recordType) {
        this.path = path.toAbsolutePath().normalize();
        this.lockPath = this.path.resolveSibling(this.path.getFileName() + ".lock");
        this.listType = JSON.getTypeFactory().constructCollectionType(List.class, recordType);
    }

    Path path() {
        return path;
    }

    /** Returns the records last written; a reader never sees a partly written file. */
    List<T> readRecords() {
        if (!Files.exists(path)) {
            return List.of();
        }
        try {
            return JSON.readValue(path.toFile(), listType);
        } catch (final IOException failure) {
            throw new UncheckedIOException(failure);
        }
    }

    /**
     * Applies one change under the lock.
     *
     * @param change receives the current records and returns the records to write and a result
     * @return the change's result
     */
    <R> R changeRecords(final Function<List<T>, Change<R>> change) {
        synchronized (MONITORS.computeIfAbsent(path, ignored -> new Object())) {
            try {
                Files.createDirectories(path.getParent());
                try (FileChannel lockChannel = FileChannel.open(
                        lockPath, StandardOpenOption.CREATE, StandardOpenOption.WRITE);
                        FileLock ignored = lockChannel.lock()) {
                    final Change<R> result = change.apply(new ArrayList<>(readRecords()));
                    if (result.recordsToWrite() != null) {
                        writeAtomically(result.recordsToWrite());
                    }
                    return result.result();
                }
            } catch (final IOException failure) {
                throw new UncheckedIOException(failure);
            }
        }
    }

    private void writeAtomically(final List<?> records) throws IOException {
        final Path temporary = Files.createTempFile(path.getParent(), path.getFileName().toString(), ".tmp");
        try {
            final ByteBuffer content = ByteBuffer.wrap(JSON.writeValueAsBytes(records));
            try (FileChannel channel = FileChannel.open(temporary, StandardOpenOption.WRITE)) {
                while (content.hasRemaining()) {
                    channel.write(content);
                }
                channel.force(true);
            }
            Files.move(temporary, path, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
        } finally {
            Files.deleteIfExists(temporary);
        }
    }
}
