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

import io.grpc.Context;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.PollWorkerSlotKind;
import io.superdurable.gen.PollWorkerTaskRequest;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;

import java.lang.reflect.Field;
import java.time.Duration;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;

import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.RPC_ONLY;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.STEP_AND_RPC;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.WorkerRun;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.concat;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.countRegisterBatches;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.eventually;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.never;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.newAppWorkerID;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.newServerDirectory;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireFirstAttempts;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireFlowsCompleted;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireNoPermanentRegistrationError;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireRegisterBatchesPerWindow;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireRegisteredStreams;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireSlotState;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.requireSlotStateWithin;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.startFlows;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.startFlowsConcurrently;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.startParkedOnlyWorker;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.startWorker;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.stepRegisters;
import static io.superdurable.dex.PullWorkerAdaptiveSlotSupport.windowsToFloor;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

// Real-server tests of adaptive pull Worker slot streams; they follow design Tests 1 to 20.
@Tag("adaptive-slots")
final class PullWorkerAdaptiveSlotStreamsTest {
    private static final Duration ONE_HOUR = Duration.ofHours(1);
    // Blocking calls need their own threads; the common pool can be smaller than an RPC burst.
    private static final ExecutorService ASYNC_CALLS = Executors.newCachedThreadPool(runnable -> {
        final Thread thread = new Thread(runnable, "dex-java-adaptive-slot-call");
        thread.setDaemon(true);
        return thread;
    });
    private final Deque<AutoCloseable> cleanups = new ArrayDeque<AutoCloseable>();

    @AfterEach
    void closeResources() throws Exception {
        Exception failure = null;
        while (!cleanups.isEmpty()) {
            try {
                cleanups.pop().close();
            } catch (Exception closeFailure) {
                if (failure == null) {
                    failure = closeFailure;
                } else {
                    failure.addSuppressed(closeFailure);
                }
            }
        }
        if (failure != null) {
            throw failure;
        }
    }

    private <T extends AutoCloseable> T track(final T resource) {
        cleanups.push(resource);
        return resource;
    }

    private AdaptiveSlotTestServer startServer(final String name, final AdaptiveSlotTestServer.Limits limits)
            throws Exception {
        return track(new AdaptiveSlotTestServer(newServerDirectory(name), limits));
    }

    private PullSlotProxy startProxy(final AdaptiveSlotTestServer server) throws Exception {
        return track(new PullSlotProxy(server.address()));
    }

    private WorkerRun startTrackedWorker(final String address, final PullWorkerAdaptiveSlotHandlers handlers,
            final PullWorkerOptions.Builder pull, final Duration shrinkWindow) {
        return track(startWorker(address, handlers, pull, shrinkWindow));
    }

    private Client newClient(final AdaptiveSlotTestServer server, final WorkerRun run) {
        return track(server.newClient(run.registry));
    }

    private static PullWorkerOptions.Builder pull(final String appWorkerID) {
        return PullWorkerOptions.newBuilder().appWorkerID(appWorkerID);
    }

    // Covers design Tests 2, 3, 4, 5, 9, and 11.
    @Test
    void growsAndShrinksWithLoad() throws Exception {
        final AdaptiveSlotTestServer server = startServer("grow", AdaptiveSlotTestServer.Limits.defaults());
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("adaptive-grow");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers,
                pull(appWorkerID).maxConcurrentTasks(16).minOpenSlots(3), Duration.ofSeconds(1));
        run.waitRegistered(Duration.ofMinutes(3));

        // Test 2: an idle adaptive Worker holds exactly L + R streams.
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        PullSlotStreamState state = run.worker.getPullSlotStreamState();
        assertTrue(state.isAdaptive());
        assertEquals(2, state.getOpen());
        assertEquals(1, state.getRpcOnlyOpen());
        assertEquals(2, state.getFloor());
        assertEquals(15, state.getEffectiveCeiling());
        assertEquals(16, state.getTotalCapacity());
        assertEquals(1, state.getRpcOnlySlots());

        // Test 3: a burst of N long Steps grows S into [N, min(E, 2N)].
        final Client client = newClient(server, run);
        final PullWorkerAdaptiveSlotFlow flow = run.flow;
        final List<String> flowIDs = new ArrayList<String>(
                startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 5));
        final List<PullWorkerAdaptiveSlotHandlers.Entry> entries =
                new ArrayList<PullWorkerAdaptiveSlotHandlers.Entry>(handlers.awaitEntries(5));
        state = requireSlotState(run, "five busy Steps",
                current -> current.getBusy() == 5 && current.getRegistering() == 0);
        assertTrue(state.getOpen() >= 5 && state.getOpen() <= 10, state::toString);

        // Test 9: RPC-only streams stay at R, and an RPC runs with every Step slot busy.
        flowIDs.addAll(startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 10));
        entries.addAll(handlers.awaitEntries(10));
        requireSlotState(run, "fifteen busy Steps", current -> current.getBusy() == 15 && current.getOpen() == 15
                && current.getRegistering() == 0);
        requireRegisteredStreams(proxy, appWorkerID, 15, 1);
        assertEquals(1, run.worker.getPullSlotStreamState().getRpcOnlyOpen());
        final PullWorkerAdaptiveSlotFlow stub = client.newRpcStub(PullWorkerAdaptiveSlotFlow.class, flowIDs.get(0));
        assertEquals("ok", CompletableFuture.supplyAsync(() -> client.invokeRPC(stub::getSlotStatus), ASYNC_CALLS)
                .get(5, TimeUnit.SECONDS));
        handlers.release.countDown();
        requireFlowsCompleted(client, flowIDs);

        // Tests 4 and 5: back to the floor under a trickle, and no Step spends a retry.
        final AtomicBoolean isTrickling = new AtomicBoolean(true);
        final CompletableFuture<List<String>> trickle = CompletableFuture.supplyAsync(() -> {
            final List<String> trickleIDs = new ArrayList<String>();
            while (isTrickling.get()) {
                trickleIDs.addAll(startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_QUICK, 1));
                try {
                    TimeUnit.MILLISECONDS.sleep(100L);
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
            return trickleIDs;
        }, ASYNC_CALLS);
        eventually("streams back at the floor", Duration.ofSeconds(windowsToFloor(15, 2) + 5L),
                () -> run.worker.getPullSlotStreamState().getOpen() == 2,
                () -> run.worker.getPullSlotStreamState().toString());
        isTrickling.set(false);
        final List<String> shortFlowIDs = trickle.get(30, TimeUnit.SECONDS);
        assertFalse(shortFlowIDs.isEmpty());
        requireFlowsCompleted(client, shortFlowIDs);
        entries.addAll(handlers.drainEntries());
        requireFirstAttempts(entries);
        state = run.worker.getPullSlotStreamState();
        assertTrue(state.getStreamsGrown() >= 13, state::toString);
        assertTrue(state.getStreamsShrunk() >= 13, state::toString);

        // Test 11: no closed stream reopens for over two maximum backoffs (2 x 7.5 s).
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        final long since = System.nanoTime();
        never("streams rose above the floor at rest, or a stream closed on purpose registered again",
                Duration.ofSeconds(16), () -> run.worker.getPullSlotStreamState().getOpen() != 2
                        || !proxy.registersSince(appWorkerID, since).isEmpty());
    }

    // Covers the RPC variant of design Test 3, where S + R serve the burst.
    @Test
    void rpcBurstUsesStepAndRpcOnlyStreams() throws Exception {
        final AdaptiveSlotTestServer server = startServer("rpc-burst", AdaptiveSlotTestServer.Limits.defaults());
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final WorkerRun run = startTrackedWorker(server.address(), handlers,
                pull(newAppWorkerID("adaptive-rpc")).maxConcurrentTasks(8).minOpenSlots(2), ONE_HOUR);
        run.waitRegistered(Duration.ofMinutes(2));
        final Client client = newClient(server, run);
        final String parkedID = "adaptive-rpc-parked-" + UUID.randomUUID();
        client.startFlow(run.parked, parkedID, null);
        final PullWorkerAdaptiveSlotParkedFlow stub =
                client.newRpcStub(PullWorkerAdaptiveSlotParkedFlow.class, parkedID);
        final int rpcCount = 6;
        final List<CompletableFuture<String>> rpcs = new ArrayList<CompletableFuture<String>>();
        for (int index = 0; index < rpcCount; index++) {
            rpcs.add(CompletableFuture.supplyAsync(() -> client.invokeRPC(stub::blockUntilReleased), ASYNC_CALLS));
        }
        handlers.awaitEntries(rpcCount);
        // E = 7 and R = 1: S + R >= min(N, E + R) and S <= min(E, 2N).
        final PullSlotStreamState state = run.worker.getPullSlotStreamState();
        assertTrue(state.getOpen() + state.getRpcOnlyOpen() >= rpcCount, state::toString);
        assertTrue(state.getOpen() <= 7, state::toString);
        assertEquals(1, state.getRpcOnlyOpen());
        handlers.release.countDown();
        for (CompletableFuture<String> rpc : rpcs) {
            assertEquals("released", rpc.get(30, TimeUnit.SECONDS));
        }
    }

    // Covers design Tests 6 and 18.
    @Test
    void growthIsSoftlyRejectedAtTheNodeLimit() throws Exception {
        final AdaptiveSlotTestServer server = startServer("node-limit", new AdaptiveSlotTestServer.Limits(20, 20));
        final PullSlotProxy proxy = startProxy(server);
        final WorkerRun fixed = track(startParkedOnlyWorker(server.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(newAppWorkerID("fixed-filler")).maxConcurrentTasks(14)));
        fixed.waitRegistered(Duration.ofMinutes(2));
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("adaptive-limit");
        final Duration window = Duration.ofSeconds(2);
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers,
                pull(appWorkerID).maxConcurrentTasks(12).minOpenSlots(3), window);
        run.waitRegistered(Duration.ofMinutes(2));
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        final Client client = newClient(server, run);
        final List<String> flowIDs = startFlows(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 10);

        // Room for 3 more: S reaches 5, and the partly rejected batch clamps G to S + H.
        final PullSlotStreamState state = requireSlotState(run, "growth clamped at the node limit",
                current -> current.getOpen() == 5 && current.getBusy() == 5 && current.getRegistering() == 0
                        && current.getGrowthTarget() == 5 && current.getGrowRejectedAtCap() > 0);
        assertEquals(11, state.getEffectiveCeiling());
        assertEquals(state.getOpen() + state.getOrphanedHandlers(), state.getGrowthTarget());
        requireNoPermanentRegistrationError(run);

        // At most one growth batch per window while rejections continue: a batch is min(E - S, S) = 5.
        final long since = System.nanoTime();
        eventually("growth retried once per window", window.multipliedBy(6),
                () -> countRegisterBatches(proxy.registersSince(appWorkerID, since), window) >= 3);
        requireRegisterBatchesPerWindow(proxy.registersSince(appWorkerID, since), window, 5);
        requireNoPermanentRegistrationError(run);

        // Room returns: growth resumes after the next window, and G returns to E.
        fixed.worker.stop();
        requireSlotState(run, "growth after the node limit cleared", current -> current.getBusy() == 10
                && current.getRegistering() == 0 && current.getGrowthTarget() == current.getEffectiveCeiling());
        handlers.release.countDown();
        requireFlowsCompleted(client, flowIDs);
        requireFirstAttempts(handlers.drainEntries());
    }

    // Covers design Test 19.
    @Test
    void floorRetryLatchClearsOnlyOnOwnStreamEndOrWindow() throws Exception {
        final AdaptiveSlotTestServer server = startServer("latch", new AdaptiveSlotTestServer.Limits(20, 20));
        final PullSlotProxy proxy = startProxy(server);
        final String appWorkerID = newAppWorkerID("adaptive-latch");
        final Duration window = Duration.ofSeconds(1);
        final WorkerRun run = startTrackedWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(appWorkerID).maxConcurrentTasks(12).reservedRpcTasks(1).minOpenSlots(3), window);
        run.waitRegistered(Duration.ofMinutes(2));
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        final PullSlotCompetitor competitor = track(new PullSlotCompetitor(server.address(),
                newAppWorkerID("competitor"), 18));
        eventually("the competitor fills the node", Duration.ofSeconds(20), () -> competitor.heldStreams() == 17,
                () -> "held " + competitor.heldStreams());

        // Resetting a floor stream frees a node slot; the competitor takes it, so the reconnect is rejected softly.
        final long since = System.nanoTime();
        assertTrue(proxy.resetRegisteredStream(appWorkerID, STEP_AND_RPC));
        requireSlotStateWithin(run, "the reset stream ended", Duration.ofSeconds(5),
                current -> current.getOpen() - current.getRegistering() == 1);
        // One reconnect after the reset, then one retry per window: wait for three window retries.
        final String[] violation = new String[1];
        eventually("the Worker retried at each window", window.multipliedBy(6), () -> {
            final PullSlotStreamState current = run.worker.getPullSlotStreamState();
            if (current.getOpen() - current.getRegistering() > 1 || !run.worker.getPullRegistrationState().isReady()) {
                violation[0] = "a stream registered at the node limit, or readiness dropped: " + current;
                return true;
            }
            return stepRegisters(proxy.registersSince(appWorkerID, since)).size() >= 4;
        });
        assertNull(violation[0]);
        final List<PullSlotProxy.ProxyEvent> registers = stepRegisters(proxy.registersSince(appWorkerID, since));
        for (int index = 1; index + 1 < registers.size(); index++) {
            assertTrue(registers.get(index + 1).atNanos - registers.get(index).atNanos >= window.toNanos() * 8 / 10,
                    "a Register followed a rejected stream's end at once");
        }
        assertEquals(1, proxy.registeredStreams(appWorkerID, STEP_AND_RPC));
        assertTrue(run.worker.getPullSlotStreamState().getGrowRejectedAtCap() >= 2);
        requireNoPermanentRegistrationError(run);

        // A competitor stream that ends does not clear this Worker's latch.
        final List<PullSlotProxy.ProxyEvent> beforeClose = stepRegisters(proxy.registersSince(appWorkerID, since));
        final PullSlotProxy.ProxyEvent lastRegisterBeforeClose = beforeClose.get(beforeClose.size() - 1);
        final long competitorClosedAt = System.nanoTime();
        competitor.closeOneStream();
        eventually("the Worker kept retrying at the window", window.multipliedBy(3),
                () -> !stepRegisters(proxy.registersSince(appWorkerID, competitorClosedAt)).isEmpty());
        final PullSlotProxy.ProxyEvent nextRegister =
                stepRegisters(proxy.registersSince(appWorkerID, competitorClosedAt)).get(0);
        assertTrue(nextRegister.atNanos - lastRegisterBeforeClose.atNanos >= window.toNanos() * 8 / 10,
                "the competitor's close triggered a Register");

        // When the competitor leaves, the floor comes back within one window.
        competitor.close();
        eventually("the floor returned", window.multipliedBy(2).plusSeconds(3), () -> {
            final PullSlotStreamState current = run.worker.getPullSlotStreamState();
            return proxy.registeredStreams(appWorkerID, STEP_AND_RPC) == 2 && current.getOpen() == 2
                    && current.getRegistering() == 0;
        });
        assertTrue(run.worker.getPullRegistrationState().isReady());
    }

    // Covers design Test 14: a fixed Worker with an unset ceiling probes and clamps.
    @Test
    void capOfOneFixedUnsetCeilingProbesAndClamps() throws Exception {
        final AdaptiveSlotTestServer server = startServer("probe", AdaptiveSlotTestServer.Limits.cap(1));
        final PullSlotProxy proxy = startProxy(server);
        final Runnable releaseAcknowledgements = proxy.holdAcknowledgements();
        final PullWorkerAdaptiveSlotSupport.PullWorkerLogCapture logs =
                track(new PullWorkerAdaptiveSlotSupport.PullWorkerLogCapture());
        final String appWorkerID = newAppWorkerID("fixed-probe");
        final WorkerRun run = startTrackedWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(appWorkerID), ONE_HOUR);
        // F4: only the probe stream registers before the first acknowledgement.
        eventually("the probe Register", Duration.ofSeconds(10),
                () -> proxy.registersSince(appWorkerID, Long.MIN_VALUE).size() == 1);
        never("a stream registered before the probe's acknowledgement", Duration.ofMillis(300),
                () -> proxy.registersSince(appWorkerID, Long.MIN_VALUE).size() > 1);
        releaseAcknowledgements.run();
        requireCapOfOneWorkerServes(server, proxy, run, appWorkerID);
        final int processors = Runtime.getRuntime().availableProcessors();
        if (processors > 1) {
            assertTrue(logs.hasWarning("clamped maxConcurrentTasks " + processors, "maxConcurrentTasksCap 1"),
                    logs::describe);
        }
        assertFalse(run.worker.getPullSlotStreamState().isAdaptive());
    }

    // Covers design Test 14: an adaptive Worker with a floor of one, with the ceiling set or not.
    @Test
    void capOfOneAdaptiveFloorOfOne() throws Exception {
        for (int maxConcurrentTasks : new int[] {4, 0}) {
            final int ceiling = maxConcurrentTasks == 0 && Runtime.getRuntime().availableProcessors() < 2
                    ? 2 : maxConcurrentTasks;
            final AdaptiveSlotTestServer server = startServer("adaptive-one", AdaptiveSlotTestServer.Limits.cap(1));
            final PullSlotProxy proxy = startProxy(server);
            final PullWorkerAdaptiveSlotSupport.PullWorkerLogCapture logs =
                    track(new PullWorkerAdaptiveSlotSupport.PullWorkerLogCapture());
            final String appWorkerID = newAppWorkerID("adaptive-one");
            final WorkerRun run = startTrackedWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                    pull(appWorkerID).maxConcurrentTasks(ceiling).minOpenSlots(1), ONE_HOUR);
            requireCapOfOneWorkerServes(server, proxy, run, appWorkerID);
            assertTrue(run.worker.getPullSlotStreamState().isAdaptive());
            assertTrue(logs.hasWarning("clamped maxConcurrentTasks", "maxConcurrentTasksCap 1"), logs::describe);
            for (PullSlotProxy.ProxyEvent register : proxy.registersSince(appWorkerID, Long.MIN_VALUE)) {
                assertEquals(STEP_AND_RPC, register.slotKind);
            }
            closeResources();
        }
    }

    // Covers design Test 14: an explicit reserved RPC slot fails start in either mode at cap 1.
    @Test
    void capOfOneExplicitReservedRpcSlotFailsStart() throws Exception {
        final boolean hasOneProcessor = Runtime.getRuntime().availableProcessors() < 2;
        final List<PullWorkerOptions.Builder> optionsByMode = new ArrayList<PullWorkerOptions.Builder>();
        optionsByMode.add(hasOneProcessor
                ? PullWorkerOptions.newBuilder().maxConcurrentTasks(2).minOpenSlots(2).reservedRpcTasks(1)
                : PullWorkerOptions.newBuilder().reservedRpcTasks(1));
        optionsByMode.add(PullWorkerOptions.newBuilder().maxConcurrentTasks(4).minOpenSlots(2).reservedRpcTasks(1));
        for (int mode = 0; mode < optionsByMode.size(); mode++) {
            final AdaptiveSlotTestServer server = startServer("reserved-one", AdaptiveSlotTestServer.Limits.cap(1));
            final WorkerRun run = startTrackedWorker(server.address(), new PullWorkerAdaptiveSlotHandlers(),
                    optionsByMode.get(mode).appWorkerID(newAppWorkerID("reserved-one")), ONE_HOUR);
            final Throwable failure = run.awaitStartResult(Duration.ofSeconds(30));
            assertTrue(failure instanceof IllegalStateException, String.valueOf(failure));
            if (mode == 0 && hasOneProcessor) {
                assertTrue(failure.getMessage().contains(
                        "maxConcurrentTasks 2 exceeds Server setting matching.maxConcurrentTasksCap 1"), failure::getMessage);
            } else {
                assertTrue(failure.getMessage().contains("reservedRpcTasks 1 must be less than the slot ceiling 1"),
                        failure::getMessage);
                assertTrue(failure.getMessage().contains("matching.maxConcurrentTasksCap 1"), failure::getMessage);
            }
            closeResources();
        }
    }

    // Covers design Test 14: a fixed Worker with an explicit ceiling above the cap fails start as today.
    @Test
    void capOfOneFixedExplicitCeilingFailsStart() throws Exception {
        final AdaptiveSlotTestServer server = startServer("fixed-two", AdaptiveSlotTestServer.Limits.cap(1));
        final WorkerRun run = startTrackedWorker(server.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(newAppWorkerID("fixed-two")).maxConcurrentTasks(2), ONE_HOUR);
        final Throwable failure = run.awaitStartResult(Duration.ofSeconds(30));
        assertTrue(failure instanceof IllegalStateException, String.valueOf(failure));
        assertTrue(failure.getMessage().contains(
                "maxConcurrentTasks 2 exceeds Server setting matching.maxConcurrentTasksCap 1"), failure::getMessage);
    }

    // Covers design Test 20a.
    @Test
    void adaptiveCeilingAboveCapIsClamped() throws Exception {
        final AdaptiveSlotTestServer server = startServer("adaptive-300", AdaptiveSlotTestServer.Limits.defaults());
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final PullWorkerAdaptiveSlotSupport.PullWorkerLogCapture logs =
                track(new PullWorkerAdaptiveSlotSupport.PullWorkerLogCapture());
        final WorkerRun run = startTrackedWorker(server.address(), handlers,
                pull(newAppWorkerID("adaptive-300")).maxConcurrentTasks(300).minOpenSlots(8), ONE_HOUR);
        run.waitRegistered(Duration.ofMinutes(4));
        assertTrue(logs.hasWarning("clamped maxConcurrentTasks 300", "maxConcurrentTasksCap 256"), logs::describe);
        assertEquals(1, logs.countWarnings("clamped maxConcurrentTasks"));
        assertEquals(256, run.worker.getPullSlotStreamState().getTotalCapacity());
        final PullWorkerAdaptiveSlotSupport.SlotUsageSampler sampler =
                track(new PullWorkerAdaptiveSlotSupport.SlotUsageSampler(run.worker));
        final Client client = newClient(server, run);
        final List<String> flowIDs = startFlowsConcurrently(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 400);
        requireSlotStateWithin(run, "every slot busy at the clamped ceiling", Duration.ofSeconds(90),
                current -> current.getBusy() == 255 && current.getOpen() + current.getRpcOnlyOpen() == 256);
        assertEquals(0L, run.worker.getPullSlotStreamState().getGrowRejectedAtCap());
        handlers.release.countDown();
        requireFlowsCompleted(client, flowIDs);
        sampler.requireWithinCapacity();
        requireFirstAttempts(handlers.drainEntries());
    }

    // Covers design Test 20b.
    @Test
    void fixedCeilingAboveCapFailsStart() throws Exception {
        final AdaptiveSlotTestServer server = startServer("fixed-300", AdaptiveSlotTestServer.Limits.defaults());
        final WorkerRun run = startTrackedWorker(server.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(newAppWorkerID("fixed-300")).maxConcurrentTasks(300), ONE_HOUR);
        final Throwable failure = run.awaitStartResult(Duration.ofSeconds(30));
        assertTrue(failure instanceof IllegalStateException, String.valueOf(failure));
        assertTrue(failure.getMessage().contains(
                "maxConcurrentTasks 300 exceeds Server setting matching.maxConcurrentTasksCap 256"), failure::getMessage);
    }

    // Covers design Test 17a.
    @Test
    void adaptiveCapDropToOneConvergesWithSoftRejection() throws Exception {
        final AdaptiveSlotTestServer server = startServer("adaptive-drop", AdaptiveSlotTestServer.Limits.cap(4));
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        handlers.isRpcStubborn = true;
        final String appWorkerID = newAppWorkerID("adaptive-drop");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers, pull(appWorkerID).maxConcurrentTasks(4)
                .minOpenSlots(2).stopOnPermanentStreamRejection(true), Duration.ofSeconds(1));
        run.waitRegistered(Duration.ofMinutes(3));
        final Client client = newClient(server, run);
        final PullWorkerAdaptiveSlotParkedFlow parked = run.parked;
        final String parkedID = "adaptive-drop-parked-" + UUID.randomUUID();
        client.startFlow(parked, parkedID, null);
        final PullWorkerAdaptiveSlotFlow flow = run.flow;
        final OccupiedSlots occupied = occupyEverySlot(run, client, flow, parkedID);

        // The restart lowers the cap to 1; the proxy lets the RPC-only stream register first.
        server.kill();
        proxy.holdStepRegistersUntilRpcOnlyAcknowledged();
        server.restart(AdaptiveSlotTestServer.Limits.cap(1));
        proxy.setUpstream(server.address());
        handlers.releaseStubborn(concat(occupied.flowIDs, PullWorkerAdaptiveSlotHandlers.STUBBORN_RPC_KEY));
        awaitRpcAfterRestart(occupied.rpcResult);

        requireSlotStateWithin(run, "one Step-and-RPC stream at cap 1", Duration.ofSeconds(30),
                current -> current.getTotalCapacity() == 1 && current.getRpcOnlySlots() == 0
                        && current.getRpcOnlyOpen() == 0 && current.getOpen() == 1 && current.getRegistering() == 0
                        && current.getOrphanedHandlers() == 0 && current.getOrphanedRpcOnlyHandlers() == 0);
        eventually("one Step-and-RPC stream on the server", Duration.ofSeconds(10),
                () -> proxy.registeredStreams(appWorkerID, STEP_AND_RPC) == 1
                        && proxy.registeredStreams(appWorkerID, RPC_ONLY) == 0, () -> proxy.describe(appWorkerID));
        assertTrue(run.worker.getPullSlotStreamState().getGrowRejectedAtCap() >= 1,
                "the Step-and-RPC Register was not rejected at the cap");
        requireNoPermanentRegistrationError(run);
        assertTrue(run.worker.getPullRegistrationState().isReady());

        // Steps and RPCs keep running on the one stream.
        final Client restartedClient = newClient(server, run);
        requireFlowsCompleted(restartedClient, occupied.flowIDs);
        requireFlowsCompleted(restartedClient,
                startFlows(restartedClient, flow, PullWorkerAdaptiveSlotHandlers.MODE_QUICK, 2));
        final PullWorkerAdaptiveSlotParkedFlow stub =
                restartedClient.newRpcStub(PullWorkerAdaptiveSlotParkedFlow.class, parkedID);
        assertEquals("ok", CompletableFuture.supplyAsync(() -> restartedClient.invokeRPC(stub::getSlotStatus), ASYNC_CALLS)
                .get(10, TimeUnit.SECONDS));
    }

    // Covers design Test 17b: fixed mode keeps today's behavior.
    @Test
    void fixedCapDropToOneKeepsPermanentRejection() throws Exception {
        for (boolean shouldStop : new boolean[] {true, false}) {
            final AdaptiveSlotTestServer server = startServer("fixed-drop", AdaptiveSlotTestServer.Limits.cap(4));
            final PullSlotProxy proxy = startProxy(server);
            final String appWorkerID = newAppWorkerID("fixed-drop");
            final WorkerRun run = startTrackedWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                    pull(appWorkerID).maxConcurrentTasks(4).stopOnPermanentStreamRejection(shouldStop),
                    Duration.ofSeconds(1));
            run.waitRegistered(Duration.ofMinutes(2));
            requireRegisteredStreams(proxy, appWorkerID, 3, 1);
            server.kill();
            proxy.holdStepRegistersUntilRpcOnlyAcknowledged();
            server.restart(AdaptiveSlotTestServer.Limits.cap(1));
            proxy.setUpstream(server.address());
            if (shouldStop) {
                final Throwable failure = run.awaitStartResult(Duration.ofSeconds(45));
                assertTrue(failure instanceof PullRegistrationError, String.valueOf(failure));
                final PullRegistrationError rejection = (PullRegistrationError) failure;
                assertEquals(STEP_AND_RPC.name(), rejection.getQueue());
                assertEquals(Status.Code.RESOURCE_EXHAUSTED, Status.fromThrowable(rejection.getCause()).getCode());
            } else {
                eventually("a permanent Step-and-RPC rejection", Duration.ofSeconds(45), () -> {
                    final PullQueueRegistrationState queue = run.worker.getPullRegistrationState().getQueues().get(0);
                    return !queue.hasRegisteredStream() && queue.getLastPermanentError() != null
                            && Status.fromThrowable(queue.getLastPermanentError()).getCode()
                                    == Status.Code.RESOURCE_EXHAUSTED;
                });
                assertFalse(run.worker.getPullRegistrationState().isReady());
                // Today's permanent-error backoff is one minute, so no Step-and-RPC stream registers again soon.
                final long rejectedAt = System.nanoTime();
                never("a Step-and-RPC stream retried before the permanent-error backoff", Duration.ofSeconds(5),
                        () -> !stepRegisters(proxy.registersSince(appWorkerID, rejectedAt)).isEmpty());
                assertFalse(run.worker.getPullSlotStreamState().isAdaptive());
                assertEquals(0L, run.worker.getPullSlotStreamState().getGrowRejectedAtCap());
            }
            closeResources();
        }
    }

    // Covers design Tests 15 and 8 with the cap unchanged.
    @Test
    void orphanedHandlersHoldCapacityAcrossRestart() throws Exception {
        final OrphanedHandlerScenario scenario = startOrphanedHandlerScenario();
        final long killedAt = System.nanoTime();
        scenario.server.kill();
        scenario.server.restart(AdaptiveSlotTestServer.Limits.cap(4));
        scenario.proxy.setUpstream(scenario.server.address());
        // No early registration: every slot still has a running handler.
        requireSlotStateWithin(scenario.run, "every handler orphaned", Duration.ofSeconds(10),
                current -> current.getOpen() == 0 && current.getRpcOnlyOpen() == 0
                        && current.getOrphanedHandlers() == 3 && current.getOrphanedRpcOnlyHandlers() == 1);
        never("a stream registered while orphaned handlers filled the capacity, or readiness dropped",
                Duration.ofSeconds(1), () -> !scenario.proxy.registersSince(scenario.appWorkerID, killedAt).isEmpty()
                        || !scenario.run.worker.getPullRegistrationState().isReady());

        // Freed capacity goes to one Step-and-RPC stream first, then to the RPC-only stream.
        scenario.handlers.releaseStubborn(scenario.flowIDs.get(0));
        eventually("one Step-and-RPC stream", Duration.ofSeconds(10),
                () -> scenario.proxy.registeredStreams(scenario.appWorkerID, STEP_AND_RPC) == 1);
        scenario.handlers.releaseStubborn(PullWorkerAdaptiveSlotHandlers.STUBBORN_RPC_KEY);
        eventually("the RPC-only stream", Duration.ofSeconds(10),
                () -> scenario.proxy.registeredStreams(scenario.appWorkerID, RPC_ONLY) == 1);
        final List<PullSlotProxy.ProxyEvent> registers = scenario.proxy.registersSince(scenario.appWorkerID, killedAt);
        assertEquals(STEP_AND_RPC, registers.get(0).slotKind);
        assertEquals(RPC_ONLY, registers.get(registers.size() - 1).slotKind);
        // Test 8: the Worker re-registers at most L + R streams first.
        assertTrue(registers.size() <= 2, () -> scenario.proxy.describe(scenario.appWorkerID));
        scenario.handlers.releaseStubborn(scenario.flowIDs.subList(1, scenario.flowIDs.size()));
        awaitRpcAfterRestart(scenario.rpcResult);
        requireFlowsCompleted(newClient(scenario.server, scenario.run), scenario.flowIDs);
        assertTrue(scenario.run.worker.getPullRegistrationState().isReady());
        // With the cap unchanged, S + H + Sr + Hr <= C held at every sample.
        scenario.sampler.requireWithinCapacity();
    }

    // Covers design Test 15 with a restart that lowers the cap below the orphaned handler count.
    @Test
    void orphanedHandlersConvergeAfterCapLoweredBelowThem() throws Exception {
        final OrphanedHandlerScenario scenario = startOrphanedHandlerScenario();
        final String appWorkerID = scenario.appWorkerID;
        scenario.server.kill();
        scenario.server.restart(AdaptiveSlotTestServer.Limits.cap(2));
        scenario.proxy.setUpstream(scenario.server.address());
        requireSlotStateWithin(scenario.run, "every handler orphaned", Duration.ofSeconds(10),
                current -> current.getOrphanedHandlers() == 3 && current.getOrphanedRpcOnlyHandlers() == 1);
        final long releasedAt = System.nanoTime();
        scenario.handlers.releaseStubborn(scenario.flowIDs.get(0));
        // The first acknowledgement reveals cap 2 at usage 4; nothing registers until usage drops below 2.
        eventually("the first acknowledgement after the restart", Duration.ofSeconds(10),
                () -> scenario.proxy.acknowledgementsSince(appWorkerID, releasedAt).size() == 1);
        final long acknowledgedAt = scenario.proxy.acknowledgementsSince(appWorkerID, releasedAt).get(0).atNanos;
        requireSlotStateWithin(scenario.run, "capacity lowered to 2", Duration.ofSeconds(10),
                current -> current.getTotalCapacity() == 2 && current.getOpen() == 0 && current.getRegistering() == 0);
        scenario.handlers.releaseStubborn(scenario.flowIDs.get(1));
        requireSlotStateWithin(scenario.run, "the second handler returned", Duration.ofSeconds(10),
                current -> current.getOrphanedHandlers() == 1);
        // Usage is still 2 of 2 with one Step and one RPC-only handler running.
        never("a Register went out while usage reached the new cap", Duration.ofSeconds(1),
                () -> !scenario.proxy.registersSince(appWorkerID, acknowledgedAt + TimeUnit.MILLISECONDS.toNanos(1))
                        .isEmpty());
        scenario.handlers.releaseStubborn(scenario.flowIDs.get(2), PullWorkerAdaptiveSlotHandlers.STUBBORN_RPC_KEY);
        final PullSlotStreamState state = requireSlotStateWithin(scenario.run, "usage converged to the new cap",
                Duration.ofSeconds(20), current -> current.getOpen() == 1 && current.getRpcOnlyOpen() == 1
                        && current.getRegistering() == 0 && current.getOrphanedHandlers() == 0
                        && current.getOrphanedRpcOnlyHandlers() == 0);
        assertTrue(state.getOpen() + state.getRpcOnlyOpen() <= state.getTotalCapacity());
        requireRegisteredStreams(scenario.proxy, appWorkerID, 1, 1);
        // F5 allows the transient excess, so this case asserts convergence instead of a bound at every sample.
        System.out.println("samples above the new cap while old handlers ran: "
                + scenario.sampler.stopSampling().size());
        awaitRpcAfterRestart(scenario.rpcResult);
        requireFlowsCompleted(newClient(scenario.server, scenario.run), scenario.flowIDs);
    }

    // Covers design Test 15 with the failover landing while a growth batch registers.
    @Test
    void failoverDuringGrowthBatch() throws Exception {
        final AdaptiveSlotTestServer server = startServer("growth-failover", AdaptiveSlotTestServer.Limits.cap(8));
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("adaptive-growth-failover");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers,
                pull(appWorkerID).maxConcurrentTasks(8).minOpenSlots(3), ONE_HOUR);
        run.waitRegistered(Duration.ofMinutes(2));
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        final Runnable releaseGrowth = proxy.holdStepRegisters();
        final Client client = newClient(server, run);
        final List<String> flowIDs = startFlows(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_STUBBORN, 2);
        // Both floor streams are busy, so a growth batch of 2 waits at the proxy.
        eventually("a held growth batch", Duration.ofSeconds(20), () -> proxy.heldStepRegisters() == 2);
        requireSlotState(run, "a growth batch registering",
                current -> current.getBusy() == 2 && current.getRegistering() == 2);
        final PullWorkerAdaptiveSlotSupport.SlotUsageSampler sampler =
                track(new PullWorkerAdaptiveSlotSupport.SlotUsageSampler(run.worker));
        final long killedAt = System.nanoTime();
        server.kill();
        server.restart(AdaptiveSlotTestServer.Limits.cap(8));
        proxy.setUpstream(server.address());
        requireSlotStateWithin(run, "the busy streams orphaned", Duration.ofSeconds(10),
                current -> current.getOrphanedHandlers() == 2 && current.getRegistering() == 2
                        && current.getRpcOnlyOpen() == 1);
        requireRegisteredStreams(proxy, appWorkerID, 0, 1);
        // The in-flight batch already holds the floor, so no other Step-and-RPC stream opens.
        assertTrue(stepRegisters(proxy.registersSince(appWorkerID, killedAt)).isEmpty(),
                () -> proxy.describe(appWorkerID));
        final long releasedAt = System.nanoTime();
        releaseGrowth.run();
        // The held batch registers on the new server; retried Steps may then grow the Worker further.
        eventually("the growth batch registered after the failover", Duration.ofSeconds(20),
                () -> stepRegisters(proxy.acknowledgementsSince(appWorkerID, releasedAt)).size() >= 2
                        && proxy.registeredStreams(appWorkerID, STEP_AND_RPC) >= 2, () -> proxy.describe(appWorkerID));
        handlers.releaseStubborn(flowIDs);
        requireFlowsCompleted(newClient(server, run), flowIDs);
        requireSlotState(run, "no orphaned handlers",
                current -> current.getOrphanedHandlers() == 0 && current.getRegistering() == 0);
        sampler.requireWithinCapacity();
        requireNoPermanentRegistrationError(run);
    }

    // Covers design Test 15 with the failover landing while a shrink half-close is in flight.
    @Test
    void failoverDuringShrinkHalfCloseWithStubbornHandler() throws Exception {
        final AdaptiveSlotTestServer server = startServer("shrink-failover", AdaptiveSlotTestServer.Limits.cap(8));
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("adaptive-shrink-failover");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers,
                pull(appWorkerID).maxConcurrentTasks(8).minOpenSlots(3), Duration.ofSeconds(1));
        run.waitRegistered(Duration.ofMinutes(2));
        final Client client = newClient(server, run);
        final PullWorkerAdaptiveSlotFlow flow = run.flow;
        final List<String> flowIDs = startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 3);
        // One handler ignores cancellation, so it outlives its stream in the crash.
        final List<String> stubbornIDs = startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_STUBBORN, 1);
        requireSlotState(run, "four busy Steps", current -> current.getBusy() == 4 && current.getRegistering() == 0);
        final PullSlotProxy.HalfCloseHold halfCloses = proxy.holdHalfCloses();
        handlers.release.countDown();
        requireFlowsCompleted(client, flowIDs);
        assertTrue(halfCloses.held.poll(20, TimeUnit.SECONDS) != null, "the Worker never shrank");
        assertEquals(1, run.worker.getPullSlotStreamState().getBusy());
        // The server still holds the half-closing stream when it crashes, and the stubborn handler still runs.
        final PullWorkerAdaptiveSlotSupport.SlotUsageSampler sampler =
                track(new PullWorkerAdaptiveSlotSupport.SlotUsageSampler(run.worker));
        server.kill();
        requireSlotStateWithin(run, "the stubborn handler orphaned", Duration.ofSeconds(10),
                current -> current.getOrphanedHandlers() == 1 && current.getBusy() == 0);
        server.restart(AdaptiveSlotTestServer.Limits.cap(8));
        // Registers that reached the dead server were retries of the same runners, so count from the switch.
        final long switchedAt = System.nanoTime();
        proxy.setUpstream(server.address());
        halfCloses.release.run();
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        final java.util.function.BooleanSupplier isShrunkStreamReopened = () ->
                stepRegisters(proxy.registersSince(appWorkerID, switchedAt)).size() > 2
                        || proxy.registeredStreams(appWorkerID, STEP_AND_RPC) > 2;
        never("a stream closed by shrink reopened after the failover", Duration.ofSeconds(3), isShrunkStreamReopened);
        final PullSlotStreamState state = run.worker.getPullSlotStreamState();
        assertEquals(1, state.getOrphanedHandlers(), () -> "the orphaned handler left the capacity count early: " + state);
        assertEquals(2, state.getOpen());
        // The returning handler frees a slot, but the floor is already open, so nothing reopens.
        handlers.releaseStubborn(stubbornIDs);
        requireSlotState(run, "the orphaned handler returned", current -> current.getOrphanedHandlers() == 0
                && current.getOpen() == 2 && current.getRpcOnlyOpen() == 1 && current.getRegistering() == 0);
        never("a stream reopened when the orphaned handler returned", Duration.ofSeconds(2), isShrunkStreamReopened);
        requireFlowsCompleted(newClient(server, run), stubbornIDs);
        sampler.requireWithinCapacity();
        requireNoPermanentRegistrationError(run);
    }

    // Covers design Tests 1 and 16.
    @Test
    void fixedModeIsUnchanged() throws Exception {
        final AdaptiveSlotTestServer server = startServer("fixed-parallel", AdaptiveSlotTestServer.Limits.defaults());
        final PullSlotProxy proxy = startProxy(server);
        final Runnable releaseAcknowledgements = proxy.holdAcknowledgements();
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("fixed-parallel");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers, pull(appWorkerID).maxConcurrentTasks(4),
                Duration.ofSeconds(1));
        // Test 16: every stream opens before the first acknowledgement arrives.
        eventually("every Register before an acknowledgement", Duration.ofSeconds(10),
                () -> proxy.registersSince(appWorkerID, Long.MIN_VALUE).size() == 4);
        releaseAcknowledgements.run();
        run.waitRegistered(Duration.ofMinutes(2));
        for (PullSlotProxy.ProxyEvent register : proxy.registersSince(appWorkerID, Long.MIN_VALUE)) {
            assertTrue(register.atNanos - proxy.firstAcknowledgementAtNanos() < 0);
        }
        // Test 1: the count stays at the ceiling through load and idle periods.
        requireRegisteredStreams(proxy, appWorkerID, 3, 1);
        final Client client = newClient(server, run);
        final List<String> flowIDs = startFlows(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 3);
        handlers.awaitEntries(3);
        assertEquals(3, run.worker.getPullSlotStreamState().getOpen());
        handlers.release.countDown();
        requireFlowsCompleted(client, flowIDs);
        final long since = System.nanoTime();
        never("a fixed Worker changed its stream count at rest", Duration.ofSeconds(3),
                () -> run.worker.getPullSlotStreamState().getOpen() != 3
                        || !proxy.registersSince(appWorkerID, since).isEmpty());
        final PullSlotStreamState state = run.worker.getPullSlotStreamState();
        assertFalse(state.isAdaptive());
        assertEquals(3, state.getOpen());
        assertEquals(1, state.getRpcOnlyOpen());
        assertEquals(0L, state.getStreamsGrown());
        assertEquals(0L, state.getStreamsShrunk());
        assertTrue(proxy.registersSince(appWorkerID, since).isEmpty());
        requireRegisteredStreams(proxy, appWorkerID, 3, 1);
    }

    // Test 16 ungated: no acknowledgement is processed before the held last startup Register is written.
    @Test
    void fixedStartupWritesEveryRegisterBeforeProcessingAnAcknowledgement() throws Exception {
        final AdaptiveSlotTestServer server = startServer("startup-order", AdaptiveSlotTestServer.Limits.defaults());
        final PullSlotProxy proxy = startProxy(server);
        final String appWorkerID = newAppWorkerID("fixed-startup-order");
        final java.util.concurrent.atomic.AtomicReference<Worker> worker =
                new java.util.concurrent.atomic.AtomicReference<Worker>();
        final java.util.concurrent.atomic.AtomicReference<String> violation =
                new java.util.concurrent.atomic.AtomicReference<String>();
        final NthRegisterWriteHook lastRegister = new NthRegisterWriteHook(4, callContext -> {
            try {
                eventually("acknowledgements of the first three Registers", Duration.ofSeconds(20),
                        () -> proxy.acknowledgementsSince(appWorkerID, Long.MIN_VALUE).size() >= 3
                                && worker.get() != null);
                never("an acknowledgement was processed before the last startup Register",
                        Duration.ofMillis(500), () -> {
                            final PullSlotStreamState state = worker.get().getPullSlotStreamState();
                            return state.getIdle() != 0 || state.getRegistering() != 3 || state.getRpcOnlyOpen() != 1;
                        });
            } catch (Throwable failure) {
                violation.set(String.valueOf(failure));
            }
        });
        final WorkerRun run = track(startWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(appWorkerID).maxConcurrentTasks(4), new PullSlotSettings(ONE_HOUR, null, lastRegister)));
        worker.set(run.worker);
        run.waitRegistered(Duration.ofMinutes(1));
        eventually("the held Register was written", Duration.ofSeconds(30), () -> lastRegister.writes.get() == 4);
        assertNull(violation.get());
        requireRegisteredStreams(proxy, appWorkerID, 3, 1);
        requireSlotState(run, "every startup stream idle",
                current -> current.getIdle() == 3 && current.getRpcOnlyOpen() == 1 && current.getRegistering() == 0);
    }

    // Stop during the startup batch: acknowledgements still wait, and stop cancels the blocked Register.
    @Test
    void stopDuringFixedStartupBatch() throws Exception {
        final AdaptiveSlotTestServer server = startServer("startup-stop", AdaptiveSlotTestServer.Limits.defaults());
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("fixed-startup-stop");
        final CountDownLatch blocked = new CountDownLatch(1);
        final NthRegisterWriteHook lastRegister = new NthRegisterWriteHook(4, callContext -> {
            blocked.countDown();
            // Blocks like a stalled transport until stop cancels the call.
            while (!callContext.isCancelled()) {
                try {
                    TimeUnit.MILLISECONDS.sleep(10L);
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    return;
                }
            }
        });
        final WorkerRun run = track(startWorker(proxy.address(), handlers,
                pull(appWorkerID).maxConcurrentTasks(4).stopDrainTimeout(Duration.ofSeconds(1)),
                new PullSlotSettings(ONE_HOUR, null, lastRegister)));
        assertTrue(blocked.await(30, TimeUnit.SECONDS), "the last startup Register never started its write");
        eventually("acknowledgements of the first three Registers", Duration.ofSeconds(20),
                () -> proxy.acknowledgementsSince(appWorkerID, Long.MIN_VALUE).size() >= 3);
        assertEquals(0, run.worker.getPullSlotStreamState().getIdle());
        assertFalse(run.worker.getPullRegistrationState().isReady());
        final long stopStartedAt = System.nanoTime();
        CompletableFuture.runAsync(run.worker::stop, ASYNC_CALLS).get(15, TimeUnit.SECONDS);
        assertTrue(System.nanoTime() - stopStartedAt < TimeUnit.SECONDS.toNanos(10));
        assertNull(run.awaitStartResult(Duration.ofSeconds(10)));
        eventually("every server stream ended", Duration.ofSeconds(20),
                () -> proxy.registeredStreams(appWorkerID, STEP_AND_RPC) == 0
                        && proxy.registeredStreams(appWorkerID, RPC_ONLY) == 0, () -> proxy.describe(appWorkerID));
        assertTrue(handlers.drainEntries().isEmpty(), "a handler ran during a stopped startup");
    }

    // F5: a reconnect granted under the old cap sends no Register above a cap learned meanwhile.
    @Test
    void grantIsRevokedAfterCapDrop() throws Exception {
        final AdaptiveSlotTestServer server = startServer("revoke", AdaptiveSlotTestServer.Limits.cap(8));
        final PullSlotProxy proxy = startProxy(server);
        final String appWorkerID = newAppWorkerID("adaptive-revoke");
        final WorkerRun run = startTrackedWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(appWorkerID).maxConcurrentTasks(8).minOpenSlots(3).reservedRpcTasks(0), ONE_HOUR);
        run.waitRegistered(Duration.ofMinutes(2));
        requireRegisteredStreams(proxy, appWorkerID, 3, 0);

        // Every floor stream breaks and gets a reconnect grant under the last known cap of 8.
        server.kill();
        proxy.failStepRegistersFromNow();
        server.restart(AdaptiveSlotTestServer.Limits.cap(2));
        proxy.setUpstream(server.address());
        // One runner registers and learns cap 2 while the other two wait in their backoff.
        proxy.passNextStepRegister();
        requireSlotStateWithin(run, "the lower cap is known", Duration.ofSeconds(30),
                current -> current.getTotalCapacity() == 2);
        proxy.stopFailingStepRegisters();
        final long capKnownAt = System.nanoTime();
        // One registered stream leaves room for exactly one more; the other old grant must be revoked.
        never("a Register granted under the old cap went out above the new cap", Duration.ofSeconds(8),
                () -> stepRegisters(proxy.registersSince(appWorkerID, capKnownAt)).size() > 1
                        || !proxy.rejectionsSince(appWorkerID, capKnownAt).isEmpty());
        requireSlotState(run, "two streams at cap 2",
                current -> current.getOpen() == 2 && current.getRegistering() == 0);
        requireRegisteredStreams(proxy, appWorkerID, 2, 0);
        requireNoPermanentRegistrationError(run);
    }

    // Admission is re-checked before the Register; a later cap drop meets a soft Server rejection.
    @Test
    void capDropBetweenAdmissionAndSend() throws Exception {
        final AdaptiveSlotTestServer server = startServer("barrier", AdaptiveSlotTestServer.Limits.cap(8));
        final PullSlotProxy proxy = startProxy(server);
        final RegisterSendBarrier barrier = new RegisterSendBarrier();
        cleanups.push(barrier::release);
        final String appWorkerID = newAppWorkerID("adaptive-barrier");
        final WorkerRun run = track(startWorker(proxy.address(), new PullWorkerAdaptiveSlotHandlers(),
                pull(appWorkerID).maxConcurrentTasks(8).minOpenSlots(3).reservedRpcTasks(0),
                new PullSlotSettings(Duration.ofSeconds(1), barrier, null)));
        run.waitRegistered(Duration.ofMinutes(2));
        requireRegisteredStreams(proxy, appWorkerID, 3, 0);

        // Every floor stream breaks; each runner is admitted under the old cap of 8 and waits before its Register.
        barrier.isArmed = true;
        server.kill();
        eventually("the runners reached the Register barrier", Duration.ofSeconds(30), () -> barrier.held.get() == 3);
        server.restart(AdaptiveSlotTestServer.Limits.cap(2));
        proxy.setUpstream(server.address());
        // One runner sends, registers, and reports cap 2 while the other two stay admitted but unsent.
        barrier.passes.countDown();
        requireSlotStateWithin(run, "the lower cap is known", Duration.ofSeconds(30),
                current -> current.getTotalCapacity() == 2);
        final long capKnownAt = System.nanoTime();
        barrier.release();
        // One held attempt is revoked; a Register re-checked just before may meet the cap once, softly.
        never("a revoked attempt sent a Register, or Registers exceeded the new cap", Duration.ofSeconds(3), () -> {
            final int rejections = proxy.rejectionsSince(appWorkerID, capKnownAt).size();
            return rejections > 1 || stepRegisters(proxy.registersSince(appWorkerID, capKnownAt)).size() > 1 + rejections
                    || proxy.registeredStreams(appWorkerID, STEP_AND_RPC) > 2;
        });
        requireRegisteredStreams(proxy, appWorkerID, 2, 0);
        requireSlotState(run, "two streams at cap 2",
                current -> current.getOpen() == 2 && current.getRegistering() == 0);
        final int rejections = proxy.rejectionsSince(appWorkerID, capKnownAt).size();
        if (rejections > 0) {
            assertTrue(run.worker.getPullSlotStreamState().getGrowRejectedAtCap() >= rejections,
                    "the rejection was not handled as soft");
        }
        requireNoPermanentRegistrationError(run);
    }

    // A blocked Register write stalls neither the other streams nor stop.
    @Test
    void blockedRegisterSendDoesNotBlockOtherStreamsOrStop() throws Exception {
        final AdaptiveSlotTestServer server = startServer("blocked-send", AdaptiveSlotTestServer.Limits.cap(4));
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final BlockingRegisterWrapper blocking = new BlockingRegisterWrapper(1);
        cleanups.push(blocking::releaseAll);
        final WorkerRun run = track(startWorker(server.address(), handlers,
                pull(newAppWorkerID("adaptive-blocked-send")).maxConcurrentTasks(8).minOpenSlots(3)
                        .stopDrainTimeout(Duration.ofSeconds(1)),
                new PullSlotSettings(ONE_HOUR, null, blocking)));

        // The first Step-and-RPC Register blocks in its write; the second registers and reports cap 4.
        eventually("a blocked Register write", Duration.ofSeconds(20), () -> blocking.blockedSends.get() == 1);
        requireSlotStateWithin(run, "a cap update on another stream", Duration.ofSeconds(20),
                current -> current.getTotalCapacity() == 4 && current.getIdle() >= 1);
        final Client client = newClient(server, run);
        requireFlowsCompleted(client, startFlows(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_QUICK, 2));
        assertEquals(1, blocking.blockedSends.get(), "the blocked write returned on its own");

        // Stop finishes within its drain timeout although one write never returns by itself.
        final long stopStartedAt = System.nanoTime();
        final CompletableFuture<Void> stopped = CompletableFuture.runAsync(run.worker::stop, ASYNC_CALLS);
        stopped.get(15, TimeUnit.SECONDS);
        assertTrue(System.nanoTime() - stopStartedAt < TimeUnit.SECONDS.toNanos(10));
        assertNull(run.awaitStartResult(Duration.ofSeconds(10)));
    }

    // Covers design Test 7 for a new cap above the floor.
    @Test
    void capDropAboveTheFloorWithoutOrphans() throws Exception {
        final AdaptiveSlotTestServer server = startServer("drop-above", AdaptiveSlotTestServer.Limits.cap(8));
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("adaptive-drop-above");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers,
                pull(appWorkerID).maxConcurrentTasks(8).minOpenSlots(3), Duration.ofSeconds(1));
        run.waitRegistered(Duration.ofMinutes(2));
        final Client client = newClient(server, run);
        final List<String> flowIDs = startFlows(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 6);
        requireSlotState(run, "six busy Steps", current -> current.getBusy() == 6);
        final PullWorkerAdaptiveSlotSupport.SlotUsageSampler sampler =
                track(new PullWorkerAdaptiveSlotSupport.SlotUsageSampler(run.worker));
        server.kill();
        final long restartedAt = System.nanoTime();
        server.restart(AdaptiveSlotTestServer.Limits.cap(4));
        proxy.setUpstream(server.address());
        // C = 4 and R = 1 leave E = 3; the retried Steps grow the Worker back to E.
        requireSlotStateWithin(run, "growth up to the lower cap", Duration.ofSeconds(30),
                current -> current.getTotalCapacity() == 4 && current.getEffectiveCeiling() == 3
                        && current.getBusy() == 3 && current.getOpen() == 3 && current.getRpcOnlyOpen() == 1
                        && current.getRegistering() == 0);
        requireRegisteredStreams(proxy, appWorkerID, 3, 1);
        assertTrue(proxy.rejectionsSince(appWorkerID, restartedAt).isEmpty(), () -> proxy.describe(appWorkerID));
        assertTrue(proxy.workerResetsSince(appWorkerID, restartedAt).isEmpty(), "cap convergence reset a stream");
        requireNoPermanentRegistrationError(run);
        handlers.release.countDown();
        requireFlowsCompleted(newClient(server, run), flowIDs);
        sampler.requireWithinCapacity();
        assertTrue(proxy.workerResetsSince(appWorkerID, restartedAt).isEmpty(), "cap convergence reset a stream");
    }

    // Covers design Test 7 for a new cap below the floor.
    @Test
    void capDropBelowTheFloorWithoutOrphans() throws Exception {
        final AdaptiveSlotTestServer server = startServer("drop-below", AdaptiveSlotTestServer.Limits.cap(8));
        final PullSlotProxy proxy = startProxy(server);
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final String appWorkerID = newAppWorkerID("adaptive-drop-below");
        final WorkerRun run = startTrackedWorker(proxy.address(), handlers, pull(appWorkerID).maxConcurrentTasks(8)
                .minOpenSlots(5).stopOnPermanentStreamRejection(true), Duration.ofSeconds(1));
        run.waitRegistered(Duration.ofMinutes(2));
        requireRegisteredStreams(proxy, appWorkerID, 4, 1);
        server.kill();
        final long restartedAt = System.nanoTime();
        server.restart(AdaptiveSlotTestServer.Limits.cap(3));
        proxy.setUpstream(server.address());
        // C = 3 and R = 1 leave E = 2, so the floor of 4 becomes L_eff = 2.
        requireSlotStateWithin(run, "the floor limited by the lower cap", Duration.ofSeconds(30),
                current -> current.getTotalCapacity() == 3 && current.getFloor() == 2 && current.getOpen() == 2
                        && current.getRpcOnlyOpen() == 1 && current.getRegistering() == 0);
        requireRegisteredStreams(proxy, appWorkerID, 2, 1);
        assertTrue(proxy.workerResetsSince(appWorkerID, restartedAt).isEmpty(), "cap convergence reset a stream");
        requireNoPermanentRegistrationError(run);
        final Client restartedClient = newClient(server, run);
        requireFlowsCompleted(restartedClient, startFlows(restartedClient, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_QUICK, 3));
    }

    // Covers design Test 12.
    @Test
    void gracefulStopDuringGrowth() throws Exception {
        final AdaptiveSlotTestServer server = startServer("stop", AdaptiveSlotTestServer.Limits.defaults());
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final WorkerRun run = startTrackedWorker(server.address(), handlers,
                pull(newAppWorkerID("adaptive-stop")).maxConcurrentTasks(32).minOpenSlots(3), Duration.ofSeconds(1));
        run.waitRegistered(Duration.ofMinutes(2));
        final Client client = newClient(server, run);
        final List<String> flowIDs = startFlowsConcurrently(client, run.flow,
                PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 20);
        handlers.awaitEntries(4);
        final CompletableFuture<Void> stopped = CompletableFuture.runAsync(run.worker::stop, ASYNC_CALLS);
        eventually("the Worker is stopping", Duration.ofSeconds(10), () -> isStopping(run.worker));
        // Busy handlers finish during the drain; tasks that never started return without spending a retry.
        handlers.release.countDown();
        stopped.get(2, TimeUnit.MINUTES);
        assertNull(run.awaitStartResult(Duration.ofSeconds(10)));
        final WorkerRun replacement = startTrackedWorker(server.address(), handlers,
                pull(newAppWorkerID("fixed-replacement")).maxConcurrentTasks(8), ONE_HOUR);
        replacement.waitRegistered(Duration.ofMinutes(2));
        requireFlowsCompleted(client, flowIDs);
        requireFirstAttempts(handlers.drainEntries());
    }

    // Covers design Test 13 against one server.
    @Test
    void mixedFleetMovesLoadToTheAdaptiveWorker() throws Exception {
        final AdaptiveSlotTestServer server = startServer("fleet", AdaptiveSlotTestServer.Limits.defaults());
        final PullWorkerAdaptiveSlotHandlers handlers = new PullWorkerAdaptiveSlotHandlers();
        final WorkerRun fixed = startTrackedWorker(server.address(), handlers,
                pull(newAppWorkerID("fixed-fleet")).maxConcurrentTasks(4), ONE_HOUR);
        final WorkerRun adaptive = startTrackedWorker(server.address(), handlers,
                pull(newAppWorkerID("adaptive-fleet")).maxConcurrentTasks(16).minOpenSlots(2), Duration.ofSeconds(1));
        fixed.waitRegistered(Duration.ofMinutes(2));
        adaptive.waitRegistered(Duration.ofMinutes(2));
        final Client client = newClient(server, adaptive);
        final PullWorkerAdaptiveSlotFlow flow = adaptive.flow;
        requireFlowsCompleted(client, startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_QUICK, 10));
        // The fixed Worker leaves; its share moves to the adaptive Worker, which grows from its floor.
        fixed.worker.stop();
        final List<String> flowIDs = startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_BLOCK, 8);
        handlers.awaitEntries(8);
        requireSlotState(adaptive, "the adaptive Worker grew to the load", current -> current.getBusy() == 8
                && current.getOpen() >= 8 && current.getOpen() <= 15);
        handlers.release.countDown();
        requireFlowsCompleted(client, flowIDs);
        requireFirstAttempts(handlers.drainEntries());
    }

    private static boolean isStopping(final Worker worker) {
        try {
            final Field pullWorkerField = Worker.class.getDeclaredField("pullWorker");
            pullWorkerField.setAccessible(true);
            final PullWorker pullWorker = (PullWorker) pullWorkerField.get(worker);
            final Field stoppingField = PullWorker.class.getDeclaredField("isStopping");
            stoppingField.setAccessible(true);
            synchronized (pullWorker) {
                return stoppingField.getBoolean(pullWorker);
            }
        } catch (ReflectiveOperationException inaccessible) {
            throw new AssertionError("cannot read PullWorker.isStopping", inaccessible);
        }
    }

    private void requireCapOfOneWorkerServes(final AdaptiveSlotTestServer server, final PullSlotProxy proxy,
            final WorkerRun run, final String appWorkerID) throws Exception {
        run.waitRegistered(Duration.ofMinutes(1));
        requireSlotState(run, "one Step-and-RPC stream at cap 1", current -> current.getTotalCapacity() == 1
                && current.getRpcOnlySlots() == 0 && current.getOpen() == 1 && current.getRpcOnlyOpen() == 0
                && current.getRegistering() == 0);
        requireRegisteredStreams(proxy, appWorkerID, 1, 0);
        final PullRegistrationState registration = run.worker.getPullRegistrationState();
        assertTrue(registration.isReady());
        assertEquals(1, registration.getQueues().size());
        final Client client = newClient(server, run);
        requireFlowsCompleted(client, startFlows(client, run.flow, PullWorkerAdaptiveSlotHandlers.MODE_QUICK, 2));
        final String parkedID = "cap-one-parked-" + UUID.randomUUID();
        client.startFlow(run.parked, parkedID, null);
        final PullWorkerAdaptiveSlotParkedFlow stub =
                client.newRpcStub(PullWorkerAdaptiveSlotParkedFlow.class, parkedID);
        assertEquals("ok", CompletableFuture.supplyAsync(() -> client.invokeRPC(stub::getSlotStatus), ASYNC_CALLS)
                .get(30, TimeUnit.SECONDS));
    }

    // An adaptive Worker whose every slot runs a handler that ignores cancellation.
    private static final class OrphanedHandlerScenario {
        private AdaptiveSlotTestServer server;
        private PullSlotProxy proxy;
        private WorkerRun run;
        private PullWorkerAdaptiveSlotHandlers handlers;
        private String appWorkerID;
        private List<String> flowIDs;
        private CompletableFuture<String> rpcResult;
        private PullWorkerAdaptiveSlotSupport.SlotUsageSampler sampler;
    }

    private OrphanedHandlerScenario startOrphanedHandlerScenario() throws Exception {
        final OrphanedHandlerScenario scenario = new OrphanedHandlerScenario();
        scenario.server = startServer("orphan", AdaptiveSlotTestServer.Limits.cap(4));
        scenario.proxy = startProxy(scenario.server);
        scenario.handlers = new PullWorkerAdaptiveSlotHandlers();
        scenario.handlers.isRpcStubborn = true;
        scenario.appWorkerID = newAppWorkerID("adaptive-orphan");
        scenario.run = startTrackedWorker(scenario.proxy.address(), scenario.handlers,
                pull(scenario.appWorkerID).maxConcurrentTasks(4).minOpenSlots(2), ONE_HOUR);
        scenario.run.waitRegistered(Duration.ofMinutes(1));
        final Client client = newClient(scenario.server, scenario.run);
        final String parkedID = "adaptive-orphan-parked-" + UUID.randomUUID();
        client.startFlow(scenario.run.parked, parkedID, null);
        final OccupiedSlots occupied = occupyEverySlot(scenario.run, client, scenario.run.flow, parkedID);
        scenario.flowIDs = occupied.flowIDs;
        scenario.rpcResult = occupied.rpcResult;
        scenario.sampler = track(new PullWorkerAdaptiveSlotSupport.SlotUsageSampler(scenario.run.worker));
        return scenario;
    }

    private static final class OccupiedSlots {
        private final List<String> flowIDs;
        private final CompletableFuture<String> rpcResult;

        private OccupiedSlots(final List<String> flowIDs, final CompletableFuture<String> rpcResult) {
            this.flowIDs = flowIDs;
            this.rpcResult = rpcResult;
        }
    }

    // Starts E stubborn Steps, then one stubborn RPC that can only land on the RPC-only stream.
    private static OccupiedSlots occupyEverySlot(final WorkerRun run, final Client client,
            final PullWorkerAdaptiveSlotFlow flow, final String parkedID) throws Exception {
        final int ceiling = run.worker.getPullSlotStreamState().getEffectiveCeiling();
        final List<String> flowIDs = startFlows(client, flow, PullWorkerAdaptiveSlotHandlers.MODE_STUBBORN, ceiling);
        flow.handlers().awaitEntries(ceiling);
        requireSlotState(run, "every Step-and-RPC slot busy",
                current -> current.getBusy() == ceiling && current.getOpen() == ceiling);
        final PullWorkerAdaptiveSlotParkedFlow stub =
                client.newRpcStub(PullWorkerAdaptiveSlotParkedFlow.class, parkedID);
        final CompletableFuture<String> rpcResult =
                CompletableFuture.supplyAsync(() -> client.invokeRPC(stub::blockUntilReleased), ASYNC_CALLS);
        final PullWorkerAdaptiveSlotHandlers.Entry entry = flow.handlers().awaitEntries(1).get(0);
        assertEquals("rpc", entry.kind);
        return new OccupiedSlots(flowIDs, rpcResult);
    }

    // Waits for the RPC that the restart interrupted; its result depends on client retries.
    private static void awaitRpcAfterRestart(final CompletableFuture<String> rpcResult) throws InterruptedException {
        try {
            System.out.println("RPC interrupted by the restart returned: " + rpcResult.get(30, TimeUnit.SECONDS));
        } catch (java.util.concurrent.ExecutionException failed) {
            System.out.println("RPC interrupted by the restart failed: " + failed.getCause());
        } catch (java.util.concurrent.TimeoutException timedOut) {
            throw new AssertionError("the RPC interrupted by the restart did not return", timedOut);
        }
    }

    // Holds armed slot streams after their admission and before their Register is re-checked and queued.
    private static final class RegisterSendBarrier implements PullSlotSettings.RegisterSendHook {
        private volatile boolean isArmed;
        private final AtomicInteger held = new AtomicInteger();
        private final CountDownLatch passes = new CountDownLatch(1);
        private final CountDownLatch released = new CountDownLatch(1);
        private final AtomicBoolean hasPassed = new AtomicBoolean();

        @Override
        public void beforeRegisterSend(final PollWorkerSlotKind slotKind) {
            if (!isArmed) {
                return;
            }
            held.incrementAndGet();
            try {
                // One pass token lets exactly one runner through; release lets the rest through.
                while (released.getCount() > 0 && !hasPassed.get()) {
                    if (passes.await(10, TimeUnit.MILLISECONDS) && hasPassed.compareAndSet(false, true)) {
                        return;
                    }
                }
                released.await();
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
            } finally {
                held.decrementAndGet();
            }
        }

        private void release() {
            released.countDown();
        }
    }

    // Runs onHeld on the writer thread before the nth Register write, outside the Worker monitor.
    private static final class NthRegisterWriteHook implements PullSlotSettings.RequestObserverWrapper {
        private final int nth;
        private final java.util.function.Consumer<Context.CancellableContext> onHeld;
        private final AtomicInteger registers = new AtomicInteger();
        private final AtomicInteger writes = new AtomicInteger();

        private NthRegisterWriteHook(final int nth,
                final java.util.function.Consumer<Context.CancellableContext> onHeld) {
            this.nth = nth;
            this.onHeld = onHeld;
        }

        @Override
        public StreamObserver<PollWorkerTaskRequest> wrap(final StreamObserver<PollWorkerTaskRequest> requests,
                final Context.CancellableContext callContext) {
            return new StreamObserver<PollWorkerTaskRequest>() {
                @Override
                public void onNext(final PollWorkerTaskRequest request) {
                    if (request.hasRegister() && registers.incrementAndGet() == nth) {
                        onHeld.accept(callContext);
                    }
                    requests.onNext(request);
                    if (request.hasRegister()) {
                        writes.incrementAndGet();
                    }
                }

                @Override
                public void onError(final Throwable failure) {
                    requests.onError(failure);
                }

                @Override
                public void onCompleted() {
                    requests.onCompleted();
                }
            };
        }
    }

    // Makes the next Step-and-RPC Register writes block until their call is cancelled or releaseAll runs.
    private static final class BlockingRegisterWrapper implements PullSlotSettings.RequestObserverWrapper {
        private final AtomicInteger remainingBlocks;
        private final AtomicInteger blockedSends = new AtomicInteger();
        private final CountDownLatch release = new CountDownLatch(1);

        private BlockingRegisterWrapper(final int blocks) {
            this.remainingBlocks = new AtomicInteger(blocks);
        }

        @Override
        public StreamObserver<PollWorkerTaskRequest> wrap(final StreamObserver<PollWorkerTaskRequest> requests,
                final Context.CancellableContext callContext) {
            return new StreamObserver<PollWorkerTaskRequest>() {
                @Override
                public void onNext(final PollWorkerTaskRequest request) {
                    final boolean isStepRegister = request.hasRegister()
                            && request.getRegister().getSlotKind() == STEP_AND_RPC;
                    if (isStepRegister && remainingBlocks.getAndDecrement() > 0) {
                        blockedSends.incrementAndGet();
                        try {
                            // Blocks like a stalled transport; cancelling the call still ends it, as in grpc-java.
                            while (!callContext.isCancelled() && !release.await(10, TimeUnit.MILLISECONDS)) {
                                continue;
                            }
                        } catch (InterruptedException interrupted) {
                            Thread.currentThread().interrupt();
                        } finally {
                            blockedSends.decrementAndGet();
                        }
                    }
                    requests.onNext(request);
                }

                @Override
                public void onError(final Throwable failure) {
                    requests.onError(failure);
                }

                @Override
                public void onCompleted() {
                    requests.onCompleted();
                }
            };
        }

        private void releaseAll() {
            release.countDown();
        }
    }
}
