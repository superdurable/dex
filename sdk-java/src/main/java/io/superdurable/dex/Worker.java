/*
 * Legacy Materials in this file remain under their original licenses.
 * See LEGACY_NOTICES.md.
 */

/*
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications after the Legacy Cutoff are licensed under the
 * Super Durable Source License 1.0.
 * Legacy Materials remain under their original licenses.
 * See LICENSE and LEGACY_NOTICES.md.
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

    public Worker(final Registry registry, final BlobCache blobCache) {
        this(registry, blobCache, WorkerOptions.newBuilder().build());
    }

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
        this.workerService = new JavaWorkerService(dispatcher, handlers);
    }

    Registry getRegistry() {
        return registry;
    }

    public WorkerTarget getWorkerTarget() {
        return workerTarget;
    }

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
