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

package io.superdurable.dex.integ;

import io.superdurable.dex.BlobCache;
import io.superdurable.dex.BlobCacheConfig;
import io.superdurable.dex.Client;
import io.superdurable.dex.ClientOptions;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Registry;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.Worker;
import io.superdurable.dex.WorkerOptions;
import io.superdurable.dex.shared.MyDependencyService;
import io.superdurable.dex.products.engagement.EngagementFlow;
import io.superdurable.dex.products.microservices.OrchestrationFlow;
import io.superdurable.dex.products.moneytransfer.MoneyTransferFlow;
import io.superdurable.dex.products.orderprocessing.OrderProcessingFlow;
import io.superdurable.dex.products.polling.PollingFlow;
import io.superdurable.dex.primitives.step.RetryingFailureFlow;
import io.superdurable.dex.primitives.stream.StreamFlow;
import io.superdurable.dex.products.subscription.SubscriptionFlow;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Arrays;
import java.util.Comparator;
import java.util.List;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import java.util.function.Predicate;
import java.util.function.Supplier;

final class IntegEnvironment implements AutoCloseable {
    private static final long CACHE_SIZE_BYTES = 64L * 1024L * 1024L;
    private static final AtomicLong FLOW_COUNTER = new AtomicLong();

    private final Path cacheDirectory;
    private final BlobCache blobCache;
    private final Worker worker;
    private final Thread workerThread;
    private final AtomicReference<Throwable> workerFailure;
    private final Client client;
    private final MoneyTransferFlow moneyTransferFlow;
    private final OrderProcessingFlow orderProcessingFlow;
    private final EngagementFlow engagementFlow;
    private final OrchestrationFlow orchestrationFlow;
    private final PollingFlow pollingFlow;
    private final RetryingFailureFlow retryingFailureFlow;
    private final StreamFlow streamFlow;
    private final SubscriptionFlow subscriptionFlow;

    private IntegEnvironment(
            final Path cacheDirectory,
            final BlobCache blobCache,
            final Worker worker,
            final Thread workerThread,
            final AtomicReference<Throwable> workerFailure,
            final Client client,
            final MoneyTransferFlow moneyTransferFlow,
            final OrderProcessingFlow orderProcessingFlow,
            final EngagementFlow engagementFlow,
            final OrchestrationFlow orchestrationFlow,
            final PollingFlow pollingFlow,
            final RetryingFailureFlow retryingFailureFlow,
            final StreamFlow streamFlow,
            final SubscriptionFlow subscriptionFlow) {
        this.cacheDirectory = cacheDirectory;
        this.blobCache = blobCache;
        this.worker = worker;
        this.workerThread = workerThread;
        this.workerFailure = workerFailure;
        this.client = client;
        this.moneyTransferFlow = moneyTransferFlow;
        this.orderProcessingFlow = orderProcessingFlow;
        this.engagementFlow = engagementFlow;
        this.orchestrationFlow = orchestrationFlow;
        this.pollingFlow = pollingFlow;
        this.retryingFailureFlow = retryingFailureFlow;
        this.streamFlow = streamFlow;
        this.subscriptionFlow = subscriptionFlow;
    }

    static IntegEnvironment start() throws IOException {
        final String serverAddress = System.getenv().getOrDefault(
                "DEX_FLOW_SERVICE_ADDRESS",
                "127.0.0.1:8801");
        final MyDependencyService service = new MyDependencyService();
        final MoneyTransferFlow moneyTransferFlow = new MoneyTransferFlow(service);
        final OrderProcessingFlow orderProcessingFlow = new OrderProcessingFlow(service);
        final EngagementFlow engagementFlow = new EngagementFlow(service);
        final OrchestrationFlow orchestrationFlow = new OrchestrationFlow(service);
        final PollingFlow pollingFlow = new PollingFlow(service);
        final RetryingFailureFlow retryingFailureFlow = new RetryingFailureFlow();
        final StreamFlow streamFlow = new StreamFlow();
        final SubscriptionFlow subscriptionFlow = new SubscriptionFlow(service);
        final List<Flow<?>> flows = Arrays.<Flow<?>>asList(
                moneyTransferFlow,
                orderProcessingFlow,
                engagementFlow,
                orchestrationFlow,
                pollingFlow,
                retryingFailureFlow,
                streamFlow,
                subscriptionFlow);

        final Path cacheDirectory = Files.createTempDirectory("dex-java-examples-integ-");
        final int workerPort = availablePort();
        final String workerAddress = "127.0.0.1:" + workerPort;
        final Registry registry = new Registry(flows);
        final BlobCache blobCache = BlobCache.open(new BlobCacheConfig(
                cacheDirectory.toString(),
                CACHE_SIZE_BYTES));
        final Worker worker = new Worker(
                registry,
                blobCache,
                WorkerOptions.newBuilder()
                        .bindAddress(workerAddress)
                        .serverAddress(serverAddress)
                        .build());
        final AtomicReference<Throwable> workerFailure = new AtomicReference<Throwable>();
        final Thread workerThread = new Thread(() -> {
            try {
                worker.start();
            } catch (final Throwable failure) {
                workerFailure.set(failure);
            }
        }, "dex-java-examples-integ-worker");
        workerThread.setDaemon(true);
        workerThread.start();
        awaitWorker(workerPort, workerFailure);
        final Client client = new Client(
                registry,
                blobCache,
                new ClientOptions(serverAddress, worker.getWorkerTarget()));
        return new IntegEnvironment(
                cacheDirectory,
                blobCache,
                worker,
                workerThread,
                workerFailure,
                client,
                moneyTransferFlow,
                orderProcessingFlow,
                engagementFlow,
                orchestrationFlow,
                pollingFlow,
                retryingFailureFlow,
                streamFlow,
                subscriptionFlow);
    }

    Client client() {
        return client;
    }

    MoneyTransferFlow moneyTransferFlow() {
        return moneyTransferFlow;
    }

    OrderProcessingFlow orderProcessingFlow() {
        return orderProcessingFlow;
    }

    EngagementFlow engagementFlow() {
        return engagementFlow;
    }

    OrchestrationFlow orchestrationFlow() {
        return orchestrationFlow;
    }

    PollingFlow pollingFlow() {
        return pollingFlow;
    }

    RetryingFailureFlow retryingFailureFlow() {
        return retryingFailureFlow;
    }

    StreamFlow streamFlow() {
        return streamFlow;
    }

    SubscriptionFlow subscriptionFlow() {
        return subscriptionFlow;
    }

    StartFlowOptions startOptions() {
        return StartFlowOptions.newBuilder().timeout(Duration.ofHours(1)).build();
    }

    String newFlowId(final String prefix) {
        return prefix + "-" + System.nanoTime() + "-" + FLOW_COUNTER.incrementAndGet();
    }

    <T> T awaitAttribute(
            final String flowId,
            final io.superdurable.dex.Attribute<T> attribute,
            final T expected,
            final Duration timeout) throws InterruptedException {
        return awaitCondition(
                () -> client.getAttribute(flowId, attribute),
                expected::equals,
                timeout,
                "attribute did not become " + expected);
    }

    <T> T awaitCondition(
            final Supplier<T> supplier,
            final Predicate<T> predicate,
            final Duration timeout,
            final String message) throws InterruptedException {
        final long deadline = System.nanoTime() + timeout.toNanos();
        T value = null;
        while (System.nanoTime() < deadline) {
            value = supplier.get();
            if (value != null && predicate.test(value)) {
                return value;
            }
            Thread.sleep(200L);
        }
        throw new AssertionError(message + " last=" + value);
    }

    @Override
    public void close() throws InterruptedException, IOException {
        client.close();
        worker.close();
        workerThread.join(10_000L);
        blobCache.close();
        deleteRecursively(cacheDirectory);
        final Throwable failure = workerFailure.get();
        if (failure != null) {
            throw new IllegalStateException("Java examples integ Worker failed", failure);
        }
    }

    private static int availablePort() throws IOException {
        try (ServerSocket socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static void awaitWorker(
            final int workerPort,
            final AtomicReference<Throwable> workerFailure) throws IOException {
        final long deadline = System.nanoTime() + 10_000_000_000L;
        while (System.nanoTime() < deadline) {
            final Throwable failure = workerFailure.get();
            if (failure != null) {
                throw new IllegalStateException("Java examples integ Worker failed", failure);
            }
            try (Socket socket = new Socket()) {
                socket.connect(new InetSocketAddress("127.0.0.1", workerPort), 100);
                return;
            } catch (final IOException unavailable) {
                Thread.yield();
            }
        }
        throw new IOException("Java examples integ Worker did not become ready");
    }

    private static void deleteRecursively(final Path path) throws IOException {
        if (path == null || !Files.exists(path)) {
            return;
        }
        Files.walk(path)
                .sorted(Comparator.reverseOrder())
                .forEach(entry -> {
                    try {
                        Files.deleteIfExists(entry);
                    } catch (final IOException ignored) {
                        // best-effort cleanup of temp cache
                    }
                });
    }
}
