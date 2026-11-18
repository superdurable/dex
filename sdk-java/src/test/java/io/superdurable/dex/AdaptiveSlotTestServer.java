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
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.StatusRuntimeException;
import io.superdurable.gen.FlowServiceGrpc;

import java.io.IOException;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.concurrent.TimeUnit;

// Runs one dex-server-zd process per test, so a test can set caps, kill, and restart it.
final class AdaptiveSlotTestServer implements AutoCloseable {
    private final String binary;
    private final Path dataDirectory;
    private final Path logPath;
    private Process process;
    private String address;

    // Matching limits; zero keeps the server default.
    static final class Limits {
        final int maxConcurrentTasksCap;
        final int maxStreams;

        Limits(final int maxConcurrentTasksCap, final int maxStreams) {
            this.maxConcurrentTasksCap = maxConcurrentTasksCap;
            this.maxStreams = maxStreams;
        }

        static Limits defaults() {
            return new Limits(0, 0);
        }

        static Limits cap(final int maxConcurrentTasksCap) {
            return new Limits(maxConcurrentTasksCap, 0);
        }
    }

    AdaptiveSlotTestServer(final Path dataDirectory, final Limits limits) throws Exception {
        final String configured = System.getenv("DEX_SERVER_ZD_BINARY");
        if (configured == null || configured.isEmpty()) {
            throw new IllegalStateException("DEX_SERVER_ZD_BINARY must name dex-server-zd; run ./run-integration-tests.sh");
        }
        this.binary = configured;
        this.dataDirectory = dataDirectory;
        this.logPath = dataDirectory.resolve("server.log");
        launch(limits);
    }

    String address() {
        return address;
    }

    private void launch(final Limits limits) throws Exception {
        try (ServerSocket reservation = new ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"))) {
            address = "127.0.0.1:" + reservation.getLocalPort();
        }
        final StringBuilder configuration = new StringBuilder(
                "dev:\n  durabilityMode: processCrashSafe\nmatching:\n  pingInterval: 2s\n");
        if (limits.maxConcurrentTasksCap != 0) {
            configuration.append("  maxConcurrentTasksCap: ").append(limits.maxConcurrentTasksCap).append('\n');
        }
        if (limits.maxStreams != 0) {
            configuration.append("  maxStreams: ").append(limits.maxStreams).append('\n');
        }
        final Path configurationPath = dataDirectory.resolve("adaptive-slot-config.yaml");
        Files.write(configurationPath, configuration.toString().getBytes(StandardCharsets.UTF_8));
        process = new ProcessBuilder(binary, "dev", "--data-dir", dataDirectory.resolve("data").toString(),
                "--listen", address, "--config", configurationPath.toString())
                .redirectErrorStream(true)
                .redirectOutput(ProcessBuilder.Redirect.appendTo(logPath.toFile()))
                .start();
        awaitServing();
    }

    private void awaitServing() throws Exception {
        final ManagedChannel channel = ManagedChannelBuilder.forTarget(address).usePlaintext().build();
        try {
            final FlowServiceGrpc.FlowServiceBlockingStub service = FlowServiceGrpc.newBlockingStub(channel);
            final long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(30);
            while (true) {
                try {
                    service.withDeadlineAfter(1, TimeUnit.SECONDS).getServerInfo(Empty.getDefaultInstance());
                    return;
                } catch (StatusRuntimeException notServing) {
                    if (!process.isAlive()) {
                        throw new IllegalStateException("dex-server-zd exited before serving; see " + logPath);
                    }
                    if (System.nanoTime() > deadline) {
                        throw new IllegalStateException("dex-server-zd did not serve; see " + logPath, notServing);
                    }
                    // The process accepts connections only after it starts serving.
                    TimeUnit.MILLISECONDS.sleep(50L);
                }
            }
        } finally {
            channel.shutdownNow();
            channel.awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    // Ends the server like a crash: every stream breaks and nothing drains.
    void kill() throws InterruptedException {
        process.destroyForcibly();
        if (!process.waitFor(30, TimeUnit.SECONDS)) {
            throw new IllegalStateException("dex-server-zd did not exit after SIGKILL");
        }
    }

    // Starts a new process on the same data directory, on a new address.
    void restart(final Limits limits) throws Exception {
        launch(limits);
    }

    Client newClient(final Registry registry) {
        return new Client(registry, new AdaptiveSlotTestBlobCache(), new ClientOptions(address));
    }

    String logTail() {
        try {
            final List<String> lines = Files.readAllLines(logPath, StandardCharsets.UTF_8);
            return String.join("\n", lines.subList(Math.max(0, lines.size() - 200), lines.size()));
        } catch (IOException unreadable) {
            return "cannot read dex-server-zd log: " + unreadable;
        }
    }

    @Override
    public void close() throws InterruptedException {
        if (!process.isAlive()) {
            return;
        }
        process.destroy();
        if (!process.waitFor(10, TimeUnit.SECONDS)) {
            kill();
        }
    }
}
