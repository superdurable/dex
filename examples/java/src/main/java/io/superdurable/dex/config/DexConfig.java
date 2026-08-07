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

package io.superdurable.dex.config;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Registry;
import io.superdurable.dex.Worker;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.WorkerTarget;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.context.event.ApplicationReadyEvent;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.event.EventListener;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

@Configuration
public class DexConfig {

    @Bean
    public Registry registry(final List<Flow<?>> flows) {
        return new Registry(new ArrayList<Flow<?>>(flows));
    }

    @Bean(destroyMethod = "close")
    public BlobCache blobCache(
            @Value("${dex.blob-cache-dir}") final String blobCacheDir) {
        return BlobCache.open(new BlobCacheConfig(blobCacheDir, 1L << 30));
    }

    @Bean(destroyMethod = "close")
    public Worker worker(
            final Registry registry,
            final BlobCache blobCache,
            @Value("${dex.server-address}") final String serverAddress,
            @Value("${dex.worker-bind-address}") final String workerBindAddress,
            @Value("${dex.worker-target:}") final String workerTarget) {
        final WorkerOptions.Builder options = WorkerOptions.newBuilder()
                .bindAddress(workerBindAddress)
                .serverAddress(serverAddress);
        if (workerTarget != null && !workerTarget.trim().isEmpty()) {
            options.workerTarget(new WorkerTarget(workerTarget, false));
        }
        return new Worker(registry, blobCache, options.build());
    }

    @Bean(destroyMethod = "close")
    public Client client(
            final Registry registry,
            final BlobCache blobCache,
            final Worker worker,
            @Value("${dex.server-address}") final String serverAddress) {
        return new Client(
                registry,
                blobCache,
                new ClientOptions(serverAddress, worker.getWorkerTarget()));
    }

    @Component
    public static class WorkerLifecycle {
        private final Worker worker;

        public WorkerLifecycle(final Worker worker) {
            this.worker = worker;
        }

        @EventListener(ApplicationReadyEvent.class)
        public void startWorker() {
            final Thread workerThread = new Thread(worker::start, "dex-java-examples-worker");
            workerThread.setDaemon(true);
            workerThread.start();
        }
    }
}
