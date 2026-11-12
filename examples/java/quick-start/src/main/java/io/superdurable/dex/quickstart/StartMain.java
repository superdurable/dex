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
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Registry;

import java.nio.file.Paths;
import java.util.List;
import java.util.UUID;

/** Starts one OrderFlow. Pass an order ID, or let the command make one. */
public final class StartMain {
    private StartMain() {
    }

    public static void main(final String[] args) throws Exception {
        final String orderId = args.length > 0
                ? args[0]
                : "order-" + UUID.randomUUID().toString().substring(0, 8);
        final OrderFlow orderFlow = new OrderFlow(new ShopApi());
        final Registry registry = new Registry(List.of(orderFlow));
        try (BlobCache cache = QuickStartConfig.openCache(Paths.get(".dex-blob-cache", "client"));
                Client client = new Client(
                        registry, cache, new ClientOptions(QuickStartConfig.SERVER_ADDRESS))) {
            client.startFlow(orderFlow, orderId, orderId);
            System.out.println("Started Flow " + orderId + ". Ship runs in "
                    + QuickStartConfig.SHIP_DELAY.toSeconds() + " seconds.");
        }
    }
}
