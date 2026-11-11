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

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.fail;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.Registry;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.TimerId;
import io.superdurable.dex.WaitForStepCompletionOptions;
import io.superdurable.dex.Worker;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import org.junit.jupiter.api.Assumptions;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.UUID;

/**
 * Runs the Quick Start against a real Dex server.
 *
 * <p>Point DEX_FLOW_SERVICE_ADDRESS at the server (default 127.0.0.1:8801). Without a server the
 * test fails, unless DEX_EXAMPLES_ALLOW_SKIP=1 asks it to skip.
 */
class QuickStartTest {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(60);

    @TempDir
    Path tempDir;

    @Test
    void restartedWorkerShipsWithoutChargingAgain() throws Exception {
        final String orderId = "order-" + UUID.randomUUID().toString().replace("-", "");
        final ShopApi firstShop = new ShopApi();
        final ShopApi secondShop = new ShopApi();
        final OrderFlow clientFlow = new OrderFlow(new ShopApi());
        final FlowResult result;
        try (BlobCache cache = QuickStartConfig.openCache(tempDir.resolve("client"));
                Client client = new Client(new Registry(List.of(clientFlow)), cache,
                        new ClientOptions(QuickStartConfig.SERVER_ADDRESS))) {
            requireServer(client);
            try (RunningWorker worker = RunningWorker.start(
                    new OrderFlow(firstShop), tempDir.resolve("worker-1"))) {
                client.startFlow(clientFlow, orderId, orderId);
                client.waitForStepCompletion(
                        orderId,
                        StepExecutionId.of("Charge"),
                        WaitForStepCompletionOptions.newBuilder().requestTimeout(WAIT_TIMEOUT).build());
            }
            assertEquals(List.of(orderId), firstShop.charged());
            assertEquals(List.of(), firstShop.shipped());

            try (RunningWorker worker = RunningWorker.start(
                    new OrderFlow(secondShop), tempDir.resolve("worker-2"))) {
                skipShipDelay(client, orderId);
                result = client.waitForFlow(orderId, WAIT_TIMEOUT);
            }
        }

        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(List.of(), secondShop.charged());
        assertEquals(List.of(orderId), secondShop.shipped());
    }

    /** Fails, or skips with DEX_EXAMPLES_ALLOW_SKIP=1, when no Dex server answers. */
    private static void requireServer(final Client client) throws InterruptedException {
        final long deadline = System.nanoTime() + Duration.ofSeconds(20).toNanos();
        while (System.nanoTime() < deadline) {
            try {
                client.describeFlow("quick-start-health-" + UUID.randomUUID());
                return;
            } catch (final FlowNotFoundException serverAnswered) {
                return;
            } catch (final DexServiceException unavailable) {
                Thread.sleep(250L);
            }
        }
        final String message = "no Dex server at " + QuickStartConfig.SERVER_ADDRESS
                + "; set DEX_FLOW_SERVICE_ADDRESS";
        Assumptions.assumeFalse("1".equals(System.getenv("DEX_EXAMPLES_ALLOW_SKIP")), message);
        fail(message);
    }

    /** Skips the 30-second Timer once Ship waits on it. */
    private static void skipShipDelay(final Client client, final String orderId)
            throws InterruptedException {
        final long deadline = System.nanoTime() + WAIT_TIMEOUT.toNanos();
        while (true) {
            try {
                client.skipTimer(
                        orderId,
                        StepExecutionId.of("Ship"),
                        TimerId.byConditionId(OrderFlow.SHIP_DELAY_TIMER));
                return;
            } catch (final DexServiceException notWaitingYet) {
                if (System.nanoTime() >= deadline) {
                    throw notWaitingYet;
                }
                Thread.sleep(250L);
            }
        }
    }

    /** A pull Worker on its own thread, stopped on close. */
    private static final class RunningWorker implements AutoCloseable {
        private final BlobCache cache;
        private final Worker worker;
        private final Thread thread;

        private RunningWorker(final BlobCache cache, final Worker worker, final Thread thread) {
            this.cache = cache;
            this.worker = worker;
            this.thread = thread;
        }

        static RunningWorker start(final OrderFlow orderFlow, final Path cacheDir) throws Exception {
            final BlobCache cache = QuickStartConfig.openCache(cacheDir);
            final Worker worker = new Worker(
                    new Registry(List.of(orderFlow)),
                    cache,
                    WorkerOptions.newBuilder().serverAddress(QuickStartConfig.SERVER_ADDRESS).build());
            final Thread thread = new Thread(worker::start, "quick-start-worker");
            thread.setDaemon(true);
            final RunningWorker running = new RunningWorker(cache, worker, thread);
            try {
                thread.start();
                worker.waitPullRegistered(Duration.ofSeconds(30));
                return running;
            } catch (final Exception failure) {
                running.close();
                throw failure;
            }
        }

        @Override
        public void close() throws InterruptedException {
            worker.close();
            thread.join(Duration.ofSeconds(10).toMillis());
            cache.close();
        }
    }
}
