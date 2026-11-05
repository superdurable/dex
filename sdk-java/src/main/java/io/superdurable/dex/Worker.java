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
import io.grpc.Codec;
import io.grpc.DecompressorRegistry;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.ServerInfo;
import io.superdurable.gen.SyncAttributeIndexRequest;

import java.time.Duration;
import java.util.concurrent.ArrayBlockingQueue;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicInteger;

/**
 * Hosts synchronous Java Step and RPC implementations for registered Flows.
 *
 * <p>The worker opens outbound pull streams to Dex. It dispatches
 * invocations on a bounded JVM executor.
 * User methods are ordinary blocking Java methods; slow external I/O occupies a handler thread and
 * should respect the configured Step or RPC timeout. Call {@link #start} on a dedicated application
 * thread because it blocks until the worker stops. A worker can be started only once. The supplied
 * {@link BlobCache} is borrowed and remains the caller's responsibility.
 *
 * <pre>{@code
 * Worker worker = new Worker(registry, blobCache, WorkerOptions.newBuilder()
 *         .serverAddress("dex:8801")
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
    private final ExecutorService handlers;
    private final ScheduledExecutorService bufferedStreamScheduler;
    private final ManagedChannel flowChannel;
    private final FlowServiceGrpc.FlowServiceBlockingStub flowService;
    private final PullWorker pullWorker;
    private final String sdkVersion;
    private final NegotiatedServerProtocol serverProtocol = new NegotiatedServerProtocol();
    private State state = State.CREATED;
    private boolean hasStartCalled;

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
     * Creates a worker with explicit polling, connection, and serialization options.
     *
     * @param registry the nonnull registry of Flow implementations
     * @param blobCache the nonnull cache used to hydrate blob-backed invocation values
     * @param options the nonnull worker options
     * @throws IllegalArgumentException if any argument is {@code null} or the pull Worker ID
     *     conflicts with a registered Flow type
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
        this.handlers = newHandlerExecutor(options.getPull().getMaxConcurrentTasks());
        this.bufferedStreamScheduler = Executors.newSingleThreadScheduledExecutor(runnable -> {
            final Thread thread = new Thread(runnable, "dex-java-buffered-stream");
            thread.setDaemon(true);
            return thread;
        });
        this.flowChannel = createOutboundChannel(options.getServerAddress());
        final FlowServiceGrpc.FlowServiceBlockingStub serviceStub =
                FlowServiceGrpc.newBlockingStub(flowChannel);
        this.flowService = options.getCredential() == null ? serviceStub
                : serviceStub.withCallCredentials(new GrpcCredential(options.getCredential()));
        this.sdkVersion = ServerProtocolCompatibility.sdkVersion();
        final ValueMapper values = new ValueMapper(options.getObjectMapper());
        final WorkerDispatcher dispatcher = new WorkerDispatcher(
                registry,
                values,
                new ValueHydrator(flowService, blobCache, options.getCredential()),
                bufferedStreamScheduler,
                serverProtocol);
        try {
            this.pullWorker = new PullWorker(
                    registry, new WorkerErrorMapper(options.getGrpcErrorStatusMapping()),
                    dispatcher, handlers, flowChannel,
                    options.getPull(), options.getCredential(), sdkVersion);
        } catch (RuntimeException failure) {
            shutdownResources();
            throw failure;
        }
    }

    Registry getRegistry() {
        return registry;
    }

    /**
     * Starts the worker pull streams and blocks until termination.
     *
     * <p>Only a newly created worker may start. It requires Server protocol 4 before syncing
     * Attribute indexes and opening the pull streams. If the waiting thread is interrupted, the worker
     * stops and restores the thread's interruption status before returning.
     *
     * @throws PullRegistrationError if an uncovered permanent stream rejection stops the Worker
     *     with stopOnPermanentStreamRejection enabled, before or after readiness
     * @throws IllegalStateException if the worker was already started or startup fails
     */
    public void start() {
        synchronized (this) {
            if (hasStartCalled || state != State.CREATED) {
                throw new IllegalStateException("Worker cannot start from state " + state);
            }
            hasStartCalled = true;
        }
        Throwable startFailure = null;
        try {
            startInternal();
        } catch (RuntimeException | Error failure) {
            startFailure = failure;
            pullWorker.finishRegistration(failure);
            shutdownResources();
            synchronized (this) {
                state = State.STOPPED;
            }
            throw failure;
        } finally {
            pullWorker.finishRegistration(startFailure);
        }
    }

    /**
     * Waits for a STEP_AND_RPC stream and, when reserved, an RPC_ONLY stream to register.
     *
     * <p>Start the Worker on another thread. Multiple callers may wait independently, before or
     * after start. Success latches even after stop; use getPullRegistrationState for later probes.
     * Before readiness, a permanent rejection fails this wait only if that slot kind has no other
     * registered stream. Startup failures are propagated to waiters. Interrupting this wait leaves
     * the Worker running.
     *
     * <pre>{@code
     * Thread workerThread = new Thread(worker::start);
     * workerThread.start();
     * worker.waitPullRegistered(Duration.ofSeconds(30));
     * boolean isReady = worker.getPullRegistrationState().isReady();
     * }</pre>
     *
     * @param timeout the nonnull, nonnegative maximum wait duration
     * @throws InterruptedException if the waiting thread is interrupted
     * @throws TimeoutException if the timeout expires before readiness
     * @throws PullRegistrationError if a slot kind is permanently rejected before readiness
     * @throws IllegalArgumentException if timeout is null or negative
     * @throws IllegalStateException if startup fails, including when maxConcurrentTasks exceeds
     *     the Server's per-Worker limit, or the Worker stops before readiness
     */
    public void waitPullRegistered(final Duration timeout) throws InterruptedException, TimeoutException {
        if (timeout == null || timeout.isNegative()) {
            throw new IllegalArgumentException("timeout must be nonnull and nonnegative");
        }
        pullWorker.waitRegistered(timeout);
    }

    /**
     * Returns zero for the legacy reserved-RPC decline counter.
     *
     * <p>Reserved execution slots prevent capacity declines. This method remains safe to call
     * concurrently and after shutdown.
     *
     * @return always zero
     * @deprecated reserved slots no longer decline tasks for capacity
     */
    @Deprecated
    public long getPullReservedRpcDeclines() {
        return pullWorker.getReservedRpcDeclines();
    }

    /**
     * Returns an independent snapshot for pull Worker readiness probes.
     *
     * <p>After initial readiness, transient failures, reconnects, and stop keep readiness true.
     * A permanent rejection makes it false only while that slot kind has no registered stream.
     * Re-registration clears the error. Safe concurrently with start, stop, and registration waits.
     * See waitPullRegistered for a startup example.
     *
     * @return immutable slot-kind state through the legacy queue accessors
     */
    public PullRegistrationState getPullRegistrationState() {
        return pullWorker.registrationState();
    }

    private void startInternal() {
        synchronized (this) {
            if (state != State.CREATED) {
                throw new IllegalStateException("Worker cannot start from state " + state);
            }
            final ServerInfo serverInfo;
            final FlowServiceGrpc.FlowServiceBlockingStub serverInfoService = flowService
                    .withDeadlineAfter(options.getAttributeIndexSyncTimeout().toMillis(),
                            TimeUnit.MILLISECONDS);
            try {
                serverInfo = GrpcCredential.callUnaryWithRetry(options.getCredential(),
                        () -> serverInfoService.getServerInfo(Empty.getDefaultInstance()));
            } catch (RuntimeException failure) {
                if (Status.fromThrowable(failure).getCode() == Status.Code.UNAUTHENTICATED) {
                    throw authenticationFailure(failure);
                }
                throw ServerProtocolCompatibility.requestFailure(sdkVersion, failure);
            }
            serverProtocol.recordNegotiatedVersion(
                    ServerProtocolCompatibility.negotiate(serverInfo, sdkVersion));
            final FlowServiceGrpc.FlowServiceBlockingStub attributeIndexService = flowService
                    .withDeadlineAfter(options.getAttributeIndexSyncTimeout().toMillis(),
                            TimeUnit.MILLISECONDS);
            try {
                GrpcCredential.callUnaryWithRetry(options.getCredential(), () -> attributeIndexService
                        .syncAttributeIndexes(SyncAttributeIndexRequest.newBuilder()
                                .putAllAttributeIndexes(registry.getAttributeIndexes())
                                .build()));
            } catch (RuntimeException failure) {
                if (Status.fromThrowable(failure).getCode() == Status.Code.UNAUTHENTICATED) {
                    throw authenticationFailure(failure);
                }
                throw new IllegalStateException("cannot synchronize Attribute indexes", failure);
            }
            state = State.RUNNING;
        }

        try {
            pullWorker.runBlocking();
        } finally {
            if (Thread.currentThread().isInterrupted()) {
                stop();
            }
        }
    }

    /**
     * Gracefully stops the pull streams, handler executor, and server channel.
     *
     * <p>The method is idempotent. The Worker drains for the configured stop timeout, then cancels
     * remaining handlers and waits for them to return. A handler ignoring cancellation can block
     * Stop. Interruption status is preserved.
     *
     * <p>Stop starts no new task. Idle streams half-close immediately and drop later task frames.
     * Busy streams finish their handler and send its final output before half-closing. Streams keep
     * reading until the Server ends them. If the timeout expires, a running task's stream is reset
     * with a transport error instead of half-closing. A half-closed stream that the Server has not
     * ended by then is also reset, for example after a network partition. A task the Server sent
     * on such a stream counts as one that may have run.
     *
     * <p>A stream that has sent a final output is not reset at the timeout until the Server
     * confirms that output. The Server confirms it by ending the stream or by assigning the
     * stream's next task. This holds even when the handler has returned or the stream has
     * half-closed. Such a stream gets a five-second flush grace and is reset only after it. So
     * stop can return up to five seconds after the stop timeout, plus the time handlers take to
     * return after cancellation.
     */
    public void stop() {
        synchronized (this) {
            if (state == State.STOPPED || state == State.CLOSED) {
                return;
            }
            state = State.STOPPING;
        }
        shutdownResources();
        synchronized (this) {
            if (state != State.CLOSED) {
                state = State.STOPPED;
            }
        }
    }

    /**
     * Stops the worker with {@link #stop()} drain semantics and permanently closes its lifecycle.
     *
     * <p>The application owns any supplied {@link Credential} and the borrowed
     * {@link BlobCache}.
     */
    @Override
    public void close() {
        stop();
        synchronized (this) {
            state = State.CLOSED;
        }
    }

    private void shutdownResources() {
        if (pullWorker != null) {
            pullWorker.stop();
        }
        handlers.shutdown();
        try {
            if (!handlers.awaitTermination(30, TimeUnit.SECONDS)) {
                handlers.shutdownNow();
            }
        } catch (InterruptedException interruption) {
            handlers.shutdownNow();
            Thread.currentThread().interrupt();
        }
        bufferedStreamScheduler.shutdownNow();
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

    private static IllegalStateException authenticationFailure(final RuntimeException cause) {
        final String description = Status.fromThrowable(cause).getDescription();
        return new IllegalStateException("cannot authenticate Worker with Dex"
                + (description == null ? "" : ": " + description), cause);
    }

    private static ExecutorService newHandlerExecutor(final int concurrency) {
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

    // Accepting only identity encoding keeps a decompression failure from cancelling a poll stream.
    private static ManagedChannel createOutboundChannel(final String address) {
        return ManagedChannelBuilder.forTarget(address).usePlaintext()
                .decompressorRegistry(DecompressorRegistry.emptyInstance().with(Codec.Identity.NONE, true))
                .build();
    }
}
