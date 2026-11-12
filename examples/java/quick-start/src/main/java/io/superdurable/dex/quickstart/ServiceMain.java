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

import com.sun.net.httpserver.HttpServer;
import io.superdurable.dex.BlobCache;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Registry;
import io.superdurable.dex.Worker;
import io.superdurable.dex.WorkerOptions;

import java.nio.file.Paths;
import java.time.Duration;
import java.util.List;

/** Serves POST /orders and runs the Worker in the same process. Stop it with Ctrl+C. */
public final class ServiceMain {
    private ServiceMain() {
    }

    public static void main(final String[] args) throws Exception {
        final OrderFlow orderFlow = new OrderFlow(QuickStartConfig.newShopApi());
        final Registry registry = new Registry(List.of(orderFlow));
        final BlobCache workerCache = QuickStartConfig.openCache(Paths.get(".dex-blob-cache", "worker"));
        final BlobCache clientCache = QuickStartConfig.openCache(Paths.get(".dex-blob-cache", "client"));
        final Worker worker = new Worker(
                registry,
                workerCache,
                WorkerOptions.newBuilder().serverAddress(QuickStartConfig.SERVER_ADDRESS).build());
        final Client client = new Client(
                registry, clientCache, new ClientOptions(QuickStartConfig.SERVER_ADDRESS));
        final HttpServer server = OrderService.createServer(client, orderFlow, QuickStartConfig.HTTP_PORT);
        final Thread workerThread = new Thread(worker::start, "dex-worker");
        workerThread.setDaemon(true);
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            System.out.println("Stopping the service.");
            server.stop(0);
            client.close();
            worker.close();
            clientCache.close();
            workerCache.close();
        }));
        workerThread.start();
        worker.waitPullRegistered(Duration.ofSeconds(30));
        server.start();
        System.out.println("Serving POST /orders on port " + QuickStartConfig.HTTP_PORT
                + " (process " + ProcessHandle.current().pid() + ").");
        workerThread.join();
    }
}
