/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.quickstart;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;

/** Settings shared by the Worker, the start command, and the test. */
public final class QuickStartConfig {
    public static final String SERVER_ADDRESS = System.getenv()
            .getOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801");

    private QuickStartConfig() {
    }

    /** Opens a local cache for large Flow values, up to 1 GiB. */
    public static BlobCache openCache(final Path directory) throws IOException {
        Files.createDirectories(directory);
        return BlobCache.open(new BlobCacheConfig(directory.toString(), 1L << 30));
    }
}
