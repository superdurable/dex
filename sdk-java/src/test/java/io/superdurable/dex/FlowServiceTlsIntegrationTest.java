/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import com.google.protobuf.Empty;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.gen.FlowServiceGrpc;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.MessageDigest;
import java.time.Duration;
import java.util.Collections;
import java.util.UUID;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;

@Tag("dex-dev")
final class FlowServiceTlsIntegrationTest {
    @TempDir
    Path directory;

    @Test
    void startsFlowsAndRunsPullStepsWithBearerCredentialsOverTls() throws Exception {
        final String token = UUID.randomUUID().toString();
        final Path tokenFile = directory.resolve("token");
        Files.write(tokenFile, token.getBytes(StandardCharsets.US_ASCII));
        final Credential credential = new FileCredential(tokenFile.toString());
        final TlsTestCertificates certificates = TlsTestCertificates.create(directory, "real-server");
        final TlsTransportFlow flow = new TlsTransportFlow();
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(flow));
        try (ServerZdProcess server = new ServerZdProcess(directory, token);
                TlsTestProxy proxy = new TlsTestProxy(certificates, "127.0.0.1", server.port, "h2");
                TokenEndpoint tokenEndpoint = new TokenEndpoint(token);
                BlobCache cache = BlobCache.open(new BlobCacheConfig(directory.resolve("cache").toString(), 64L << 20))) {
            final String address = "localhost:" + proxy.port();
            final FlowServiceTlsOptions tls = FlowServiceTlsOptions.newBuilder()
                    .additionalRootCAsPem(certificates.caPem).serverName("dex.test").build();
            try (ApiKeyCredential apiKeyCredential = ApiKeyCredential.builder(
                    tokenEndpoint.uri(), "tls-test", "tls-test-api-key").build();
                    Client client = new Client(registry, cache, ClientOptions.newBuilder().serverAddress(address)
                            .flowServiceTls(tls).credential(apiKeyCredential).build());
                    Worker worker = new Worker(registry, cache, WorkerOptions.newBuilder().serverAddress(address)
                            .flowServiceTls(tls).credential(credential).build())) {
                final AtomicReference<Throwable> workerFailure = new AtomicReference<Throwable>();
                final Thread workerThread = new Thread(() -> startWorker(worker, workerFailure), "dex-java-tls-test");
                workerThread.start();
                try {
                    worker.waitPullRegistered(Duration.ofSeconds(30));
                    final String flowId = "java-tls-" + UUID.randomUUID();
                    client.startFlow(flow, flowId, "tls-output");
                    assertEquals("tls-output", client.waitForFlow(flowId, Duration.ofSeconds(30))
                            .getSingleOutput(String.class));
                } finally {
                    worker.stop();
                    workerThread.join(15_000);
                    assertFalse(workerThread.isAlive());
                    assertNull(workerFailure.get());
                }
            }
            assertTrue(proxy.hasHandshake("dex.test", "h2"), proxy.handshakeDescriptions());
            assertTrue(tokenEndpoint.exchanges.get() > 0);
            try (Client anonymous = new Client(registry, cache,
                    ClientOptions.newBuilder().serverAddress(address).flowServiceTls(tls).build())) {
                assertEquals(Status.Code.UNAUTHENTICATED, assertThrows(DexServiceException.class,
                        () -> anonymous.startFlow(flow, "java-tls-anonymous-" + UUID.randomUUID(), "value"))
                        .getCode());
            }
            assertClientUnavailable(registry, cache, address, credential, FlowServiceTlsOptions.newBuilder().build());
            final FlowServiceTlsOptions wrongName = FlowServiceTlsOptions.newBuilder()
                    .additionalRootCAsPem(certificates.caPem).serverName("wrong.example").build();
            assertClientUnavailable(registry, cache, address, credential, wrongName);
            try (Worker rejected = new Worker(registry, cache, WorkerOptions.newBuilder().serverAddress(address)
                    .flowServiceTls(wrongName).credential(credential)
                    .attributeIndexSyncTimeout(Duration.ofSeconds(5)).build())) {
                assertThrows(IllegalStateException.class, rejected::start);
            }
            try (Client plaintext = new Client(registry, cache, ClientOptions.newBuilder()
                    .serverAddress("127.0.0.1:" + server.port).credential(credential).build())) {
                assertNotNull(plaintext.searchFlows("", 1));
            }
        }
    }

    private static void startWorker(final Worker worker, final AtomicReference<Throwable> failure) {
        try {
            worker.start();
        } catch (Throwable caught) {
            failure.set(caught);
        }
    }

    private static void assertClientUnavailable(final Registry registry, final BlobCache cache,
            final String address, final Credential credential, final FlowServiceTlsOptions tls) {
        try (Client rejected = new Client(registry, cache, ClientOptions.newBuilder().serverAddress(address)
                .flowServiceTls(tls).credential(credential).build())) {
            final io.grpc.Context.CancellableContext deadline = io.grpc.Context.current()
                    .withDeadlineAfter(5, TimeUnit.SECONDS, io.grpc.netty.shaded.io.netty.util.concurrent.GlobalEventExecutor.INSTANCE);
            try {
                deadline.run(() -> assertEquals(Status.Code.UNAVAILABLE,
                        assertThrows(DexServiceException.class, () -> rejected.searchFlows("", 1)).getCode()));
            } finally {
                deadline.cancel(null);
            }
        }
    }

    private static final class TlsTransportFlow implements Flow<String> {
        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(new TlsTransportStep());
        }
    }

    private static final class TlsTransportStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class ServerZdProcess implements AutoCloseable {
        private final Process process;
        private final int port;

        private ServerZdProcess(final Path directory, final String token) throws Exception {
            final String binary = System.getenv("DEX_SERVER_ZD_BINARY");
            if (binary == null || binary.isEmpty()) {
                throw new IllegalStateException("DEX_SERVER_ZD_BINARY must name dex-server-zd; run ./run-integration-tests.sh");
            }
            try (ServerSocket reservation = new ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1"))) {
                port = reservation.getLocalPort();
            }
            final byte[] tokenHash = MessageDigest.getInstance("SHA-256").digest(token.getBytes(StandardCharsets.US_ASCII));
            final StringBuilder digest = new StringBuilder();
            for (final byte value : tokenHash) {
                digest.append(String.format("%02x", value & 0xff));
            }
            final Path tokenHashes = directory.resolve("token-hashes.yaml");
            Files.write(tokenHashes, ("tokens:\n  - sha256: " + digest + "\n").getBytes(StandardCharsets.US_ASCII));
            final Path config = directory.resolve("server-zd.yaml");
            Files.write(config, ("auth:\n  authenticators:\n    - kind: staticTokens\n      file: " + tokenHashes
                    + "\ndev:\n  durabilityMode: processCrashSafe\n").getBytes(StandardCharsets.UTF_8));
            process = new ProcessBuilder(binary, "dev", "--data-dir", directory.resolve("zd").toString(),
                    "--listen", "127.0.0.1:" + port, "--config", config.toString())
                    .redirectErrorStream(true).redirectOutput(directory.resolve("server-zd.log").toFile()).start();
            final ManagedChannel channel = ManagedChannelBuilder.forTarget("127.0.0.1:" + port).usePlaintext().build();
            try {
                awaitReady(channel, token);
            } catch (Exception failure) {
                close();
                throw new IllegalStateException("TLS integration server did not become ready; see "
                        + directory.resolve("server-zd.log"), failure);
            } finally {
                channel.shutdownNow();
                assertTrue(channel.awaitTermination(5, TimeUnit.SECONDS));
            }
        }

        private static void awaitReady(final ManagedChannel channel, final String token) throws Exception {
            final java.util.concurrent.ScheduledExecutorService scheduler =
                    java.util.concurrent.Executors.newSingleThreadScheduledExecutor();
            final ServerReadinessProbe probe = new ServerReadinessProbe(FlowServiceGrpc.newBlockingStub(channel)
                    .withCallCredentials(new GrpcCredential(() -> token)));
            final java.util.concurrent.ScheduledFuture<?> polling = scheduler.scheduleWithFixedDelay(
                    probe, 0, 100, TimeUnit.MILLISECONDS);
            try {
                probe.ready.get(60, TimeUnit.SECONDS);
            } finally {
                polling.cancel(true);
                scheduler.shutdownNow();
                assertTrue(scheduler.awaitTermination(5, TimeUnit.SECONDS));
            }
        }

        @Override
        public void close() throws InterruptedException {
            process.destroy();
            if (!process.waitFor(15, TimeUnit.SECONDS)) {
                process.destroyForcibly();
                assertTrue(process.waitFor(5, TimeUnit.SECONDS));
            }
        }
    }

    private static final class ServerReadinessProbe implements Runnable {
        private final FlowServiceGrpc.FlowServiceBlockingStub service;
        private final java.util.concurrent.CompletableFuture<Void> ready = new java.util.concurrent.CompletableFuture<Void>();

        private ServerReadinessProbe(final FlowServiceGrpc.FlowServiceBlockingStub service) {
            this.service = service;
        }

        @Override
        public void run() {
            try {
                service.withDeadlineAfter(2, TimeUnit.SECONDS).getServerInfo(Empty.getDefaultInstance());
                ready.complete(null);
            } catch (StatusRuntimeException failure) {
                if (failure.getStatus().getCode() != Status.Code.UNAVAILABLE) {
                    ready.completeExceptionally(failure);
                }
            }
        }
    }

    private static final class TokenEndpoint implements AutoCloseable {
        private final HttpServer server;
        private final String token;
        private final AtomicInteger exchanges = new AtomicInteger();

        private TokenEndpoint(final String token) throws IOException {
            this.token = token;
            server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
            server.createContext("/token", this::exchange);
            server.start();
        }

        private URI uri() {
            return URI.create("http://127.0.0.1:" + server.getAddress().getPort() + "/token");
        }

        private void exchange(final HttpExchange exchange) throws IOException {
            exchanges.incrementAndGet();
            final byte[] response = ("{\"access_token\":\"" + token
                    + "\",\"token_type\":\"Bearer\",\"expires_in\":3600}").getBytes(StandardCharsets.UTF_8);
            exchange.getResponseHeaders().set("Content-Type", "application/json");
            exchange.sendResponseHeaders(200, response.length);
            try (OutputStream output = exchange.getResponseBody()) {
                output.write(response);
            } finally {
                exchange.close();
            }
        }

        @Override
        public void close() {
            server.stop(0);
        }
    }
}
