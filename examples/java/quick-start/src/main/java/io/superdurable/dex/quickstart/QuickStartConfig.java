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
import java.time.Duration;

/** Settings shared by the Worker, the start command, the service, and the test. */
public final class QuickStartConfig {
    public static final String SERVER_ADDRESS = System.getenv()
            .getOrDefault("DEX_FLOW_SERVICE_ADDRESS", "127.0.0.1:8801");
    public static final int HTTP_PORT = Integer.parseInt(System.getenv()
            .getOrDefault("QUICK_START_HTTP_PORT", "8080"));
    /** How long Ship waits, from QUICK_START_SHIP_DELAY_SECONDS; 30 seconds by default. */
    public static final Duration SHIP_DELAY = Duration.ofSeconds(Long.parseLong(System.getenv()
            .getOrDefault("QUICK_START_SHIP_DELAY_SECONDS", "30")));
    private static final double CHARGE_DELAY_SECONDS = Double.parseDouble(System.getenv()
            .getOrDefault("QUICK_START_CHARGE_DELAY_SECONDS", "0"));

    private QuickStartConfig() {
    }

    /** Returns the mock API that the Worker calls, with its keys in a local file. */
    public static ShopApi newShopApi() {
        return new ShopApi(Path.of(".shop-api-keys"),
                Duration.ofMillis(Math.round(CHARGE_DELAY_SECONDS * 1000)));
    }

    /** Opens a local cache for large Flow values, up to 1 GiB. */
    public static BlobCache openCache(final Path directory) throws IOException {
        Files.createDirectories(directory);
        return BlobCache.open(new BlobCacheConfig(directory.toString(), 1L << 30));
    }
}
