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

import io.superdurable.gen.PollWorkerSlotKind;

import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;
import java.util.concurrent.TimeUnit;
import java.util.function.BooleanSupplier;
import java.util.function.Predicate;
import java.util.function.Supplier;
import java.util.logging.Handler;
import java.util.logging.Level;
import java.util.logging.LogRecord;
import java.util.logging.Logger;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

// Shared helpers of the adaptive slot stream real-server tests.
final class PullWorkerAdaptiveSlotSupport {
    static final PollWorkerSlotKind STEP_AND_RPC = PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC;
    static final PollWorkerSlotKind RPC_ONLY = PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY;

    private PullWorkerAdaptiveSlotSupport() {
    }

    // Keeps server logs in the Gradle build directory for failure analysis.
    static Path newServerDirectory(final String name) throws Exception {
        final Path root = Paths.get("build", "adaptive-slot-servers");
        Files.createDirectories(root);
        return Files.createTempDirectory(root.toAbsolutePath(), name + "-");
    }

    static String newAppWorkerID(final String prefix) {
        return prefix + "-" + UUID.randomUUID().toString().substring(0, 8);
    }

    // Polls condition every 10 ms until it holds or timeout ends.
    static void eventually(final String description, final Duration timeout, final BooleanSupplier condition,
            final Supplier<String> detail) throws InterruptedException {
        final long deadline = System.nanoTime() + timeout.toNanos();
        while (!condition.getAsBoolean()) {
            if (System.nanoTime() - deadline > 0) {
                throw new AssertionError("never reached " + description + ": " + detail.get());
            }
            TimeUnit.MILLISECONDS.sleep(10L);
        }
    }

    static void eventually(final String description, final Duration timeout, final BooleanSupplier condition)
            throws InterruptedException {
        eventually(description, timeout, condition, () -> "");
    }

    // Polls condition every 10 ms for the whole duration and fails as soon as it holds.
    static void never(final String description, final Duration duration, final BooleanSupplier condition)
            throws InterruptedException {
        final long deadline = System.nanoTime() + duration.toNanos();
        while (System.nanoTime() - deadline < 0) {
            if (condition.getAsBoolean()) {
                throw new AssertionError(description);
            }
            TimeUnit.MILLISECONDS.sleep(10L);
        }
    }

    static PullSlotStreamState requireSlotState(final WorkerRun run, final String description,
            final Predicate<PullSlotStreamState> condition) throws InterruptedException {
        return requireSlotStateWithin(run, description, Duration.ofSeconds(30), condition);
    }

    static PullSlotStreamState requireSlotStateWithin(final WorkerRun run, final String description,
            final Duration timeout, final Predicate<PullSlotStreamState> condition) throws InterruptedException {
        final PullSlotStreamState[] last = new PullSlotStreamState[1];
        eventually("slot streams " + description, timeout, () -> {
            last[0] = run.worker.getPullSlotStreamState();
            return condition.test(last[0]);
        }, () -> String.valueOf(last[0]));
        return last[0];
    }

    static void requireRegisteredStreams(final PullSlotProxy proxy, final String appWorkerID, final int stepStreams,
            final int rpcOnlyStreams) throws InterruptedException {
        eventually("server streams of " + appWorkerID + ": " + stepStreams + " Step-and-RPC and "
                        + rpcOnlyStreams + " RPC-only", Duration.ofSeconds(20),
                () -> proxy.registeredStreams(appWorkerID, STEP_AND_RPC) == stepStreams
                        && proxy.registeredStreams(appWorkerID, RPC_ONLY) == rpcOnlyStreams,
                () -> "have " + proxy.registeredStreams(appWorkerID, STEP_AND_RPC) + " and "
                        + proxy.registeredStreams(appWorkerID, RPC_ONLY) + "\n" + proxy.describe(appWorkerID));
    }

    static void requireNoPermanentRegistrationError(final WorkerRun run) {
        final PullRegistrationState registration = run.worker.getPullRegistrationState();
        assertTrue(registration.isReady(), "readiness dropped");
        for (PullQueueRegistrationState queue : registration.getQueues()) {
            assertNull(queue.getLastPermanentError(), "slot kind " + queue.getQueue() + " recorded a permanent error");
        }
        assertTrue(!run.started.isDone(), () -> "start returned during a soft rejection: " + run.started.getNow(null));
    }

    static List<PullSlotProxy.ProxyEvent> stepRegisters(final List<PullSlotProxy.ProxyEvent> registers) {
        final List<PullSlotProxy.ProxyEvent> stepOnly = new ArrayList<PullSlotProxy.ProxyEvent>();
        for (PullSlotProxy.ProxyEvent register : registers) {
            if (register.slotKind == STEP_AND_RPC) {
                stepOnly.add(register);
            }
        }
        return stepOnly;
    }

    // Groups Registers that arrive within half a window of a batch's first one.
    static int countRegisterBatches(final List<PullSlotProxy.ProxyEvent> registers, final Duration window) {
        int batches = 0;
        long batchStart = 0L;
        for (PullSlotProxy.ProxyEvent register : registers) {
            if (batches == 0 || register.atNanos - batchStart > window.toNanos() / 2) {
                batches++;
                batchStart = register.atNanos;
            }
        }
        return batches;
    }

    // Checks that Registers come in at most one batch per window.
    static void requireRegisterBatchesPerWindow(final List<PullSlotProxy.ProxyEvent> registers,
            final Duration window, final int batchSize) {
        long batchStart = registers.get(0).atNanos;
        int batchCount = 0;
        for (PullSlotProxy.ProxyEvent register : registers) {
            if (register.atNanos - batchStart > window.toNanos() / 2) {
                assertTrue(register.atNanos - batchStart >= window.toNanos() * 8 / 10, "two growth batches in one window");
                batchStart = register.atNanos;
                batchCount = 0;
            }
            batchCount++;
            assertTrue(batchCount <= batchSize, "a growth batch exceeded " + batchSize);
        }
    }

    // Invariant 5: ceil(log2(E - L + 1)) + 1 windows.
    static int windowsToFloor(final int effectiveCeiling, final int floor) {
        int windows = 1;
        for (int span = 1; span < effectiveCeiling - floor + 1; span *= 2) {
            windows++;
        }
        return windows;
    }

    static List<String> startFlows(final Client client, final PullWorkerAdaptiveSlotFlow flow, final String mode,
            final int count) {
        final List<String> flowIDs = new ArrayList<String>();
        for (int index = 0; index < count; index++) {
            final String flowID = "adaptive-slot-" + UUID.randomUUID();
            client.startFlow(flow, flowID, mode);
            flowIDs.add(flowID);
        }
        return flowIDs;
    }

    static List<String> startFlowsConcurrently(final Client client, final PullWorkerAdaptiveSlotFlow flow,
            final String mode, final int count) throws Exception {
        final ExecutorService starters = Executors.newFixedThreadPool(16);
        try {
            final List<String> flowIDs = new ArrayList<String>();
            final List<Future<?>> started = new ArrayList<Future<?>>();
            for (int index = 0; index < count; index++) {
                final String flowID = "adaptive-slot-" + UUID.randomUUID();
                flowIDs.add(flowID);
                started.add(starters.submit(() -> client.startFlow(flow, flowID, mode)));
            }
            for (Future<?> start : started) {
                start.get(2, TimeUnit.MINUTES);
            }
            return flowIDs;
        } finally {
            starters.shutdownNow();
        }
    }

    static void requireFlowsCompleted(final Client client, final List<String> flowIDs) {
        for (String flowID : flowIDs) {
            assertEquals(FlowStatus.COMPLETED, client.waitForFlow(flowID, Duration.ofMinutes(2)).getStatus(),
                    "Flow " + flowID);
        }
    }

    static void requireFirstAttempts(final List<PullWorkerAdaptiveSlotHandlers.Entry> entries) {
        for (PullWorkerAdaptiveSlotHandlers.Entry entry : entries) {
            assertEquals(1, entry.attempt, () -> "a Step spent a retry: " + entry);
        }
    }

    static List<String> concat(final List<String> first, final String... rest) {
        final List<String> joined = new ArrayList<String>(first);
        joined.addAll(Arrays.asList(rest));
        return joined;
    }

    // Owns a started Worker and its start result; the result is the throwable start ended with, or null.
    static final class WorkerRun implements AutoCloseable {
        final Worker worker;
        final Registry registry;
        final PullWorkerAdaptiveSlotHandlers handlers;
        // The registered instances; a Client of this registry starts Flows only with them.
        final PullWorkerAdaptiveSlotFlow flow;
        final PullWorkerAdaptiveSlotParkedFlow parked;
        final CompletableFuture<Throwable> started = new CompletableFuture<Throwable>();
        private final Thread thread;

        WorkerRun(final String address, final PullWorkerAdaptiveSlotHandlers handlers,
                final PullWorkerAdaptiveSlotFlow flow, final PullWorkerAdaptiveSlotParkedFlow parked,
                final PullWorkerOptions.Builder pull, final PullSlotSettings settings) {
            this.handlers = handlers;
            this.flow = flow;
            this.parked = parked;
            final List<Flow<?>> flows = new ArrayList<Flow<?>>();
            if (flow != null) {
                flows.add(flow);
            }
            flows.add(parked);
            this.registry = new Registry(flows);
            this.worker = new Worker(registry, new AdaptiveSlotTestBlobCache(), WorkerOptions.newBuilder()
                    .serverAddress(address).pull(pull.slotSettingsForTest(settings).build()).build());
            this.thread = new Thread(() -> {
                try {
                    worker.start();
                    started.complete(null);
                } catch (Throwable failure) {
                    started.complete(failure);
                }
            }, "dex-java-adaptive-slot-worker");
            thread.start();
        }

        Throwable awaitStartResult(final Duration timeout) throws Exception {
            return started.get(timeout.toMillis(), TimeUnit.MILLISECONDS);
        }

        void waitRegistered(final Duration timeout) throws Exception {
            worker.waitPullRegistered(timeout);
        }

        @Override
        public void close() throws Exception {
            // A failed test may leave handlers that ignore cancellation; stop waits for every handler.
            handlers.releaseAllStubborn();
            handlers.release.countDown();
            worker.stop();
            thread.join(TimeUnit.SECONDS.toMillis(30));
        }
    }

    static WorkerRun startWorker(final String address, final PullWorkerAdaptiveSlotHandlers handlers,
            final PullWorkerOptions.Builder pull, final Duration shrinkWindow) {
        return startWorker(address, handlers, pull, new PullSlotSettings(shrinkWindow, null, null));
    }

    static WorkerRun startWorker(final String address, final PullWorkerAdaptiveSlotHandlers handlers,
            final PullWorkerOptions.Builder pull, final PullSlotSettings settings) {
        return new WorkerRun(address, handlers, new PullWorkerAdaptiveSlotFlow(handlers),
                new PullWorkerAdaptiveSlotParkedFlow(handlers), pull, settings);
    }

    // Serves only the parked Flow, so it never takes the other Flow's Steps.
    static WorkerRun startParkedOnlyWorker(final String address, final PullWorkerAdaptiveSlotHandlers handlers,
            final PullWorkerOptions.Builder pull) {
        return new WorkerRun(address, handlers, null, new PullWorkerAdaptiveSlotParkedFlow(handlers), pull,
                new PullSlotSettings(Duration.ofHours(1), null, null));
    }

    // Records the WARNING and INFO messages of the pull Worker logger.
    static final class PullWorkerLogCapture extends Handler implements AutoCloseable {
        private final Logger logger = Logger.getLogger(PullWorker.class.getName());
        private final List<String> warnings = new ArrayList<String>();

        PullWorkerLogCapture() {
            logger.addHandler(this);
        }

        @Override
        public synchronized void publish(final LogRecord record) {
            if (record.getLevel().intValue() >= Level.WARNING.intValue()) {
                warnings.add(record.getMessage());
            }
        }

        // Reports whether one WARNING message contains every fragment.
        synchronized boolean hasWarning(final String... fragments) {
            for (String warning : warnings) {
                boolean hasAll = true;
                for (String fragment : fragments) {
                    hasAll &= warning.contains(fragment);
                }
                if (hasAll) {
                    return true;
                }
            }
            return false;
        }

        synchronized int countWarnings(final String fragment) {
            int count = 0;
            for (String warning : warnings) {
                if (warning.contains(fragment)) {
                    count++;
                }
            }
            return count;
        }

        synchronized String describe() {
            return warnings.toString();
        }

        @Override
        public void flush() {
        }

        @Override
        public void close() {
            logger.removeHandler(this);
        }
    }

    // Records every sample where S + H + Sr + Hr exceeds C.
    static final class SlotUsageSampler implements AutoCloseable {
        private final Thread thread;
        private final List<PullSlotStreamState> violations = new ArrayList<PullSlotStreamState>();
        private volatile boolean isStopped;

        SlotUsageSampler(final Worker worker) {
            thread = new Thread(() -> {
                while (!isStopped) {
                    final PullSlotStreamState state = worker.getPullSlotStreamState();
                    if (state.getOpen() + state.getOrphanedHandlers() + state.getRpcOnlyOpen()
                            + state.getOrphanedRpcOnlyHandlers() > state.getTotalCapacity()) {
                        synchronized (this) {
                            if (violations.size() < 8) {
                                violations.add(state);
                            }
                        }
                    }
                    try {
                        TimeUnit.MILLISECONDS.sleep(5L);
                    } catch (InterruptedException interrupted) {
                        return;
                    }
                }
            }, "dex-java-adaptive-slot-sampler");
            thread.setDaemon(true);
            thread.start();
        }

        // Stops sampling and returns the samples above the capacity.
        List<PullSlotStreamState> stopSampling() throws InterruptedException {
            isStopped = true;
            thread.join();
            synchronized (this) {
                return new ArrayList<PullSlotStreamState>(violations);
            }
        }

        void requireWithinCapacity() throws InterruptedException {
            final List<PullSlotStreamState> above = stopSampling();
            assertTrue(above.isEmpty(), () -> "slot usage exceeded the capacity: " + above);
        }

        @Override
        public void close() throws InterruptedException {
            isStopped = true;
            thread.join();
        }
    }
}
