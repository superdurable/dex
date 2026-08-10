/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex;

import com.google.common.net.HostAndPort;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Server;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.superdurable.gen.FlowServiceGrpc;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Hosts synchronous Java Step and RPC implementations for registered Flows.
 *
 * <p>The worker exposes a gRPC listener to Dex and dispatches invocations on a bounded JVM executor.
 * User methods are ordinary blocking Java methods; slow external I/O occupies a handler thread and
 * should respect the configured Step or RPC timeout. Call {@link #start} on a dedicated application
 * thread because it blocks until the worker stops. A worker can be started only once. The supplied
 * {@link BlobCache} is borrowed and remains the caller's responsibility.
 *
 * <pre>{@code
 * Worker worker = new Worker(registry, blobCache, WorkerOptions.newBuilder()
 *         .bindAddress("0.0.0.0:8803")
 *         .workerTarget(new WorkerTarget("orders-worker:8803", false))
 *         .build());
 * Runtime.getRuntime().addShutdownHook(new Thread(worker::stop));
 * worker.start();
 * }</pre>
 */
public final class Worker implements AutoCloseable {
    private enum State {
        CREATED,
        RUNNING,
        STOPPING,
        STOPPED,
        CLOSED
    }

    private final Registry registry;
    private final WorkerOptions options;
    private final WorkerTarget workerTarget;
    private final ExecutorService handlers;
    private final ManagedChannel flowChannel;
    private final JavaWorkerService workerService;
    private Server server;
    private State state = State.CREATED;

    /**
     * Creates a worker using local development defaults.
     *
     * @param registry the nonnull registry of Flow implementations
     * @param blobCache the nonnull cache used to hydrate blob-backed invocation values
     * @throws IllegalArgumentException if either argument is {@code null}
     */
    public Worker(final Registry registry, final BlobCache blobCache) {
        this(registry, blobCache, WorkerOptions.newBuilder().build());
    }

    /**
     * Creates a worker with explicit listener, routing, connection, and serialization options.
     *
     * @param registry the nonnull registry of Flow implementations
     * @param blobCache the nonnull cache used to hydrate blob-backed invocation values
     * @param options the nonnull worker options
     * @throws IllegalArgumentException if any argument is {@code null}
     */
    public Worker(
            final Registry registry,
            final BlobCache blobCache,
            final WorkerOptions options) {
        if (registry == null || blobCache == null || options == null) {
            throw new IllegalArgumentException("registry, blobCache, and options are required");
        }
        this.registry = registry;
        this.options = options;
        this.workerTarget = options.getWorkerTarget() == null
                ? targetFromBindAddress(options.getBindAddress())
                : options.getWorkerTarget();
        this.handlers = newHandlerExecutor();
        this.flowChannel = ManagedChannelBuilder.forTarget(options.getServerAddress())
                .usePlaintext()
                .build();
        final FlowServiceGrpc.FlowServiceBlockingStub flowService =
                FlowServiceGrpc.newBlockingStub(flowChannel);
        final ValueMapper values = new ValueMapper(options.getObjectMapper());
        final WorkerDispatcher dispatcher = new WorkerDispatcher(
                registry,
                values,
                new ValueHydrator(flowService, blobCache));
        this.workerService = new JavaWorkerService(
                dispatcher,
                handlers,
                options.getGrpcErrorStatusMapping());
    }

    Registry getRegistry() {
        return registry;
    }

    /**
     * Returns the address this worker advertises for Dex routing.
     *
     * <p>When options do not specify a target, the worker derives one from its bind address.
     *
     * @return the effective worker target
     */
    public WorkerTarget getWorkerTarget() {
        return workerTarget;
    }

    /**
     * Starts the worker listener and blocks until termination.
     *
     * <p>Only a newly created worker may start. If the waiting thread is interrupted, the worker
     * stops and restores the thread's interruption status before returning.
     *
     * @throws IllegalStateException if the worker was already started or its address cannot bind
     */
    public void start() {
        final Server runningServer;
        synchronized (this) {
            if (state != State.CREATED) {
                throw new IllegalStateException("Worker cannot start from state " + state);
            }
            try {
                server = NettyServerBuilder.forAddress(bindAddress(options.getBindAddress()))
                        .addService(workerService)
                        .build()
                        .start();
            } catch (IOException failure) {
                state = State.STOPPED;
                shutdownResources();
                throw new IllegalStateException(
                        "cannot bind Java Worker to " + options.getBindAddress(),
                        failure);
            }
            state = State.RUNNING;
            runningServer = server;
        }

        boolean interrupted = false;
        try {
            runningServer.awaitTermination();
        } catch (InterruptedException interruption) {
            interrupted = true;
        } finally {
            stop();
            if (interrupted) {
                Thread.currentThread().interrupt();
            }
        }
    }

    /**
     * Gracefully stops the listener, handler executor, and server channel.
     *
     * <p>The method is idempotent. It waits up to 30 seconds for in-flight handlers before forcing
     * shutdown and preserves interruption status.
     */
    public void stop() {
        final Server runningServer;
        synchronized (this) {
            if (state == State.STOPPED || state == State.CLOSED) {
                return;
            }
            state = State.STOPPING;
            runningServer = server;
        }
        if (runningServer != null) {
            runningServer.shutdown();
            try {
                if (!runningServer.awaitTermination(30, TimeUnit.SECONDS)) {
                    runningServer.shutdownNow();
                    runningServer.awaitTermination(5, TimeUnit.SECONDS);
                }
            } catch (InterruptedException interruption) {
                runningServer.shutdownNow();
                Thread.currentThread().interrupt();
            }
        }
        shutdownResources();
        synchronized (this) {
            if (state != State.CLOSED) {
                state = State.STOPPED;
            }
        }
    }

    /**
     * Stops the worker and permanently closes its lifecycle.
     *
     * <p>The borrowed {@link BlobCache} is not closed.
     */
    @Override
    public void close() {
        stop();
        synchronized (this) {
            state = State.CLOSED;
        }
    }

    private void shutdownResources() {
        handlers.shutdown();
        try {
            if (!handlers.awaitTermination(30, TimeUnit.SECONDS)) {
                handlers.shutdownNow();
            }
        } catch (InterruptedException interruption) {
            handlers.shutdownNow();
            Thread.currentThread().interrupt();
        }
        flowChannel.shutdown();
        try {
            if (!flowChannel.awaitTermination(5, TimeUnit.SECONDS)) {
                flowChannel.shutdownNow();
            }
        } catch (InterruptedException interruption) {
            flowChannel.shutdownNow();
            Thread.currentThread().interrupt();
        }
    }

    private static ExecutorService newHandlerExecutor() {
        final int concurrency = Math.max(
                2,
                Math.min(32, Runtime.getRuntime().availableProcessors()));
        final int queueCapacity = concurrency * 2;
        final ClassLoader contextClassLoader = Thread.currentThread().getContextClassLoader();
        final AtomicInteger nextThread = new AtomicInteger();
        return new ThreadPoolExecutor(
                concurrency,
                concurrency,
                0L,
                TimeUnit.MILLISECONDS,
                new ArrayBlockingQueue<Runnable>(queueCapacity),
                runnable -> {
                    final Thread thread = new Thread(
                            runnable,
                            "dex-java-handler-" + nextThread.incrementAndGet());
                    thread.setContextClassLoader(contextClassLoader);
                    thread.setDaemon(true);
                    return thread;
                },
                new ThreadPoolExecutor.AbortPolicy());
    }

    private static InetSocketAddress bindAddress(final String address) {
        final HostAndPort parsed = parseAddress(address, "Worker bind address");
        return parsed.getHost().isEmpty()
                ? new InetSocketAddress(parsed.getPort())
                : new InetSocketAddress(parsed.getHost(), parsed.getPort());
    }

    private static WorkerTarget targetFromBindAddress(final String address) {
        final HostAndPort parsed = parseAddress(address, "Worker bind address");
        final String host = parsed.getHost().isEmpty()
                || "0.0.0.0".equals(parsed.getHost())
                || "::".equals(parsed.getHost())
                ? "localhost"
                : parsed.getHost();
        return new WorkerTarget(HostAndPort.fromParts(host, parsed.getPort()).toString(), false);
    }

    private static HostAndPort parseAddress(final String address, final String description) {
        if (address == null || !address.equals(address.trim())) {
            throw new IllegalArgumentException(description + " is required without whitespace");
        }
        final HostAndPort parsed;
        try {
            parsed = HostAndPort.fromString(address);
        } catch (IllegalArgumentException failure) {
            throw new IllegalArgumentException(description + " is invalid: " + address, failure);
        }
        if (!parsed.hasPort() || parsed.getPort() < 1 || parsed.getPort() > 65535) {
            throw new IllegalArgumentException(description + " requires port 1-65535");
        }
        return parsed;
    }
}
