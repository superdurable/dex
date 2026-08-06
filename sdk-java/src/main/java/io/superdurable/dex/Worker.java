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

import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;

public final class Worker implements AutoCloseable {
    private enum State {
        CREATED,
        RUNNING,
        STOPPING,
        STOPPED,
        CLOSED
    }

    private final Registry registry;
    private final BlobCache blobCache;
    private final WorkerOptions options;
    private final WorkerDispatcher dispatcher;
    private final ExecutorService handlers;
    private final long nativeHandle;
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
        this.blobCache = blobCache;
        this.options = options;
        this.dispatcher = new WorkerDispatcher(
                registry,
                new ValueMapper(options.getObjectMapper()));
        final int concurrency = Math.max(2, Runtime.getRuntime().availableProcessors());
        this.handlers = Executors.newFixedThreadPool(concurrency, runnable -> {
            final Thread thread = new Thread(runnable, "dex-java-handler");
            thread.setDaemon(true);
            return thread;
        });
        this.nativeHandle = NativeCore.create(
                registry.nativeSpecJson(options.getObjectMapper()),
                concurrency * 2);
    }

    Registry getRegistry() {
        return registry;
    }

    public void start() {
        synchronized (this) {
            if (state != State.CREATED) {
                throw new IllegalStateException("Worker cannot start from state " + state);
            }
            state = State.RUNNING;
            final int concurrency = Math.max(2, Runtime.getRuntime().availableProcessors());
            for (int index = 0; index < concurrency; index++) {
                handlers.execute(this::poll);
            }
        }
        try {
            NativeCore.serve(nativeHandle, options.getBindAddress());
        } finally {
            stop();
        }
    }

    public void stop() {
        synchronized (this) {
            if (state == State.STOPPED || state == State.CLOSED) {
                return;
            }
            state = State.STOPPING;
        }
        NativeCore.stop(nativeHandle);
        handlers.shutdown();
        try {
            if (!handlers.awaitTermination(30, TimeUnit.SECONDS)) {
                handlers.shutdownNow();
            }
        } catch (InterruptedException exception) {
            handlers.shutdownNow();
            Thread.currentThread().interrupt();
        }
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
            if (state == State.CLOSED) {
                return;
            }
            NativeCore.destroy(nativeHandle);
            state = State.CLOSED;
        }
    }

    private void poll() {
        while (!Thread.currentThread().isInterrupted()) {
            final NativeInvocation invocation;
            try {
                invocation = NativeInvocation.decode(NativeCore.poll(nativeHandle));
            } catch (IllegalStateException shutdown) {
                return;
            }
            try {
                final byte[] response = dispatcher.dispatch(invocation);
                NativeCore.complete(
                        nativeHandle,
                        invocation.getProtocolVersion(),
                        invocation.getId(),
                        true,
                        response,
                        "",
                        "");
            } catch (Throwable failure) {
                final String message = failure.getMessage() == null
                        ? failure.toString()
                        : failure.getMessage();
                NativeCore.complete(
                        nativeHandle,
                        invocation.getProtocolVersion(),
                        invocation.getId(),
                        false,
                        message.getBytes(StandardCharsets.UTF_8),
                        failure.getClass().getName(),
                        message);
            }
        }
    }
}
