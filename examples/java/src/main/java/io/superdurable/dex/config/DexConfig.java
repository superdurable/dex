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
import io.superdurable.dex.PullWorkerOptions;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.stereotype.Component;

import jakarta.annotation.PostConstruct;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.TimeoutException;

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
            @Value("${dex.server-address}") final String serverAddress) {
        return new Worker(registry, blobCache, WorkerOptions.newBuilder()
                .serverAddress(serverAddress)
                .pull(PullWorkerOptions.newBuilder().maxConcurrentTasks(64).build())
                .build());
    }

    @Bean(destroyMethod = "close")
    public Client client(
            final Registry registry,
            final BlobCache blobCache,
            @Value("${dex.server-address}") final String serverAddress) {
        return new Client(
                registry,
                blobCache,
                new ClientOptions(serverAddress, WorkerTarget.pull()));
    }

    @Component
    public static class WorkerLifecycle {
        private static final Duration PULL_REGISTRATION_TIMEOUT = Duration.ofSeconds(30);

        private final Worker worker;
        private Thread workerThread;

        public WorkerLifecycle(final Worker worker) {
            this.worker = worker;
        }

        @PostConstruct
        public void startWorker() {
            workerThread = new Thread(worker::start, "dex-java-examples-worker");
            workerThread.setDaemon(true);
            workerThread.start();
            waitForPullRegistration();
        }

        private void waitForPullRegistration() {
            try {
                worker.waitPullRegistered(PULL_REGISTRATION_TIMEOUT);
            } catch (final InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                throw new IllegalStateException(
                        "interrupted waiting for pull Worker registration", interrupted);
            } catch (final TimeoutException timeout) {
                throw new IllegalStateException("pull Worker did not register with Dex", timeout);
            }
        }
    }
}
