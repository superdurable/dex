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
import io.grpc.ManagedChannel;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.InvokeExecuteMethodOutput;
import io.superdurable.gen.InvokeWaitForMethodOutput;
import io.superdurable.gen.PollWorkerSlotKind;
import io.superdurable.gen.PollWorkerTask;
import io.superdurable.gen.PollWorkerTaskFrame;
import io.superdurable.gen.PollWorkerTaskPong;
import io.superdurable.gen.PollWorkerTaskRegister;
import io.superdurable.gen.PollWorkerTaskReject;
import io.superdurable.gen.PollWorkerTaskRejectReason;
import io.superdurable.gen.PollWorkerTaskRequest;
import io.superdurable.gen.PollWorkerTaskResponse;
import io.superdurable.gen.StepMethodHeartbeat;
import io.superdurable.gen.StepStreamWrite;
import io.superdurable.gen.WorkerTaskServiceGrpc;

import java.net.InetAddress;
import java.net.UnknownHostException;
import java.security.SecureRandom;
import java.time.Duration;
import java.time.DateTimeException;
import java.time.Instant;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.RejectedExecutionException;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import java.util.concurrent.atomic.AtomicReference;
import java.util.logging.Level;
import java.util.logging.Logger;
import java.util.function.Supplier;

final class PullWorker {
    private static final Logger LOGGER = Logger.getLogger(PullWorker.class.getName());
    private static final long BUSY_PULL_STREAM_KEEPALIVE_SECONDS = 20L;
    // Extra time after the stop timeout for a queued final output to reach Dex before reset.
    private static final Duration FINAL_OUTPUT_FLUSH_GRACE = Duration.ofSeconds(5);
    private static final char[] BASE32 = "abcdefghijklmnopqrstuvwxyz234567".toCharArray();
    private final WorkerTaskServiceGrpc.WorkerTaskServiceStub taskService;
    private final Credential credential;
    private final WorkerErrorMapper errorMapper;
    private final WorkerDispatcher dispatcher;
    private final ExecutorService handlers;
    private final ScheduledThreadPoolExecutor scheduler;
    // Runs transport writes outside the PullWorker monitor so a blocked write never delays a reset.
    private final ExecutorService streamWriters;
    private final PullWorkerOptions options;
    private final List<String> flowTypes;
    private final Map<String, Registry.RegisteredFlow> registeredFlows;
    private final List<PollWorkerSlotKind> slotKinds;
    private final List<PullStream> streams = new ArrayList<PullStream>();
    private final String appWorkerID;
    private final String taskQueue;
    private final String sdkVersion;
    private final Random jitter = new SecureRandom();
    private final Map<PollWorkerSlotKind, SlotRegistration> slotRegistrations =
            new HashMap<PollWorkerSlotKind, SlotRegistration>();
    private boolean hasRegisteredRequiredSlotKinds;
    private boolean hasStartReturned;
    private Throwable startError;
    private PullRegistrationError initialRegistrationError;
    private PullRegistrationError permanentStreamError;
    private int busyTasks;
    private boolean hasCheckedRegistrationLimit;
    private String registrationLimitFailure;
    // The resolved slot configuration; the first acknowledgement may clamp it (D4, F6).
    private int maxConcurrentTasks;
    private int reservedRpcTasks;
    private int minOpenSlots;
    private final int configuredMaxConcurrentTasks;
    private final boolean isAdaptive;
    private final PullSlotSettings slotSettings;
    // Monitor-guarded slot state: C, R_eff, L, L_eff, E, and G, with G at most E.
    private int totalCapacity;
    private int rpcOnlySlots;
    private int stepFloor;
    private int effectiveFloor;
    private int effectiveCeiling;
    private int growthTarget;
    private final int[] stepPhaseCounts = new int[SlotPhase.values().length];
    private final int[] rpcOnlyPhaseCounts = new int[SlotPhase.values().length];
    private int orphanedStepHandlers;
    private int orphanedRpcOnlyHandlers;
    private boolean isGrowthTargetClamped;
    private boolean hasGrownThisWindow;
    // The retry latch that a soft rejection sets.
    private boolean isReopenDeferred;
    private boolean hasBatchRejection;
    private int peakBusy;
    private long nextIdleSequence;
    private ScheduledFuture<?> shrinkWindowTimer;
    private long shrinkWindowGeneration;
    private long streamsGrown;
    private long streamsShrunk;
    private long growRejectedAtCap;
    private long shrinkRaceDrops;
    // Collects the explicit fixed-mode startup streams, which open in one monitor section; null otherwise.
    private List<PullStream> startupBatch;

    private static final class SlotRegistration {
        private int registeredStreams;
        private boolean hasRegistered;
        private Throwable lastPermanentError;
    }

    // The phase of one attempt; an untracked attempt counts in no total.
    private enum SlotPhase {
        UNTRACKED,
        REGISTERING,
        IDLE,
        BUSY,
        // A stream draining after the Server's Reconnect; it takes no task.
        RECONNECTING
    }

    // One registration attempt of a slot stream; the PullWorker monitor guards it.
    private static final class SlotAttempt {
        private SlotPhase phase = SlotPhase.UNTRACKED;
        private long idleSequence;
        private final boolean isGrowth;
        private final boolean isOpenedAfterReadiness;
        private boolean hasEnded;
        private boolean hasReceivedRegistered;
        // Set just before the Register is queued; until then closeSurplusSlotStreams may revoke the attempt.
        private boolean isRegisterSent;
        private boolean isOrphaned;
        private boolean isClosedOnPurpose;
        private boolean isSoftRejected;
        private boolean isReopenGranted;

        private SlotAttempt(final boolean isGrowth, final boolean isOpenedAfterReadiness) {
            this.isGrowth = isGrowth;
            this.isOpenedAfterReadiness = isOpenedAfterReadiness;
        }
    }
    private boolean isStopping;
    private boolean hasLoggedUnsupported;
    private long stopDeadlineNanos;
    private long finalOutputFlushDeadlineNanos;

    PullWorker(
            final Registry registry,
            final WorkerErrorMapper errorMapper,
            final WorkerDispatcher dispatcher,
            final ExecutorService handlers,
            final ManagedChannel channel,
            final PullWorkerOptions options,
            final Credential credential,
            final String sdkVersion) {
        this.errorMapper = errorMapper;
        this.credential = credential;
        this.dispatcher = dispatcher;
        this.handlers = handlers;
        final WorkerTaskServiceGrpc.WorkerTaskServiceStub serviceStub =
                WorkerTaskServiceGrpc.newStub(channel);
        this.taskService = credential == null ? serviceStub
                : serviceStub.withCallCredentials(new GrpcCredential(credential));
        this.options = options;
        this.sdkVersion = sdkVersion;
        this.scheduler = new ScheduledThreadPoolExecutor(1, runnable -> {
            final Thread thread = new Thread(runnable, "dex-java-pull-timer");
            thread.setDaemon(true);
            return thread;
        });
        scheduler.setRemoveOnCancelPolicy(true);
        final List<String> registered = new ArrayList<String>();
        final Map<String, Registry.RegisteredFlow> definitions =
                new HashMap<String, Registry.RegisteredFlow>();
        for (Flow<?> flow : registry.getFlows()) {
            registered.add(flow.getFlowType());
            definitions.put(flow.getFlowType(), registry.getFlow(flow.getFlowType()));
        }
        this.registeredFlows = Collections.unmodifiableMap(definitions);
        Collections.sort(registered);
        this.flowTypes = Collections.unmodifiableList(registered);
        this.appWorkerID = options.getAppWorkerID().isEmpty()
                ? generateAppWorkerID(defaultWorkerIdentity()) : options.getAppWorkerID();
        this.taskQueue = options.getTaskQueue();
        if (registered.contains(appWorkerID)) {
            scheduler.shutdownNow();
            throw new IllegalArgumentException("appWorkerID conflicts with Flow type " + appWorkerID);
        }
        this.streamWriters = Executors.newCachedThreadPool(runnable -> {
            final Thread thread = new Thread(runnable, "dex-java-pull-writer");
            thread.setDaemon(true);
            return thread;
        });
        this.slotKinds = new ArrayList<PollWorkerSlotKind>();
        slotKinds.add(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC);
        if (options.getReservedRpcTasks() > 0) {
            slotKinds.add(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY);
        }
        for (PollWorkerSlotKind slotKind : slotKinds) {
            slotRegistrations.put(slotKind, new SlotRegistration());
        }
        this.maxConcurrentTasks = options.getMaxConcurrentTasks();
        this.configuredMaxConcurrentTasks = options.getMaxConcurrentTasks();
        this.reservedRpcTasks = options.getReservedRpcTasks();
        this.minOpenSlots = options.getMinOpenSlots();
        this.isAdaptive = options.getMinOpenSlots() < options.getMaxConcurrentTasks();
        this.slotSettings = options.getSlotSettings();
        this.totalCapacity = maxConcurrentTasks;
        this.rpcOnlySlots = reservedRpcTasks;
        this.stepFloor = minOpenSlots - reservedRpcTasks;
        recomputeSlotCapacity();
    }

    private PollWorkerTaskRejectReason unknownTaskReason(final PollWorkerTask task) {
        switch (task.getRequestCase()) {
            case WAIT_FOR:
                final Registry.RegisteredFlow waitFlow =
                        registeredFlows.get(task.getWaitFor().getFlowType());
                return waitFlow != null && waitFlow.getSteps().containsKey(task.getWaitFor().getStepType())
                        ? PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED
                        : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP;
            case EXECUTE:
                final Registry.RegisteredFlow executeFlow =
                        registeredFlows.get(task.getExecute().getFlowType());
                if ("sys:timeout_handler".equals(task.getExecute().getStepType())) {
                    return executeFlow != null && executeFlow.hasTimeoutHandler()
                            ? PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED
                            : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP;
                }
                return executeFlow != null && executeFlow.getSteps().containsKey(task.getExecute().getStepType())
                        ? PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED
                        : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP;
            case RPC:
                final Registry.RegisteredFlow rpcFlow =
                        registeredFlows.get(task.getRpc().getFlowType());
                return rpcFlow != null && rpcFlow.getRpcs().containsKey(task.getRpc().getRpcName())
                        ? PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED
                        : PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_RPC;
            default:
                return PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED;
        }
    }

    String getAppWorkerID() {
        return appWorkerID;
    }

    static String defaultWorkerIdentity() {
        return defaultWorkerIdentity(System.getenv(), () -> {
            try {
                return InetAddress.getLocalHost().getHostName();
            } catch (UnknownHostException unavailable) {
                return "worker";
            }
        });
    }

    static String defaultWorkerIdentity(
            final Map<String, String> environment,
            final Supplier<String> hostname) {
        for (String environmentName : new String[] {"DEX_WORKER_INSTANCE", "POD_NAME", "HOSTNAME"}) {
            final String candidate = environment.get(environmentName);
            if (candidate != null && !candidate.trim().isEmpty()) {
                return candidate;
            }
        }
        final String resolved = hostname.get();
        return resolved == null || resolved.trim().isEmpty() ? "worker" : resolved;
    }

    static String generateAppWorkerID(final String identity) {
        final StringBuilder sanitized = new StringBuilder();
        for (char character : identity.toLowerCase(java.util.Locale.ROOT).toCharArray()) {
            if ((character >= 'a' && character <= 'z')
                    || (character >= '0' && character <= '9') || character == '-') {
                sanitized.append(character);
            } else if (sanitized.length() > 0) {
                sanitized.append('-');
            }
            if (sanitized.length() >= 54) {
                break;
            }
        }
        String prefix = sanitized.toString().replaceAll("^-+|-+$", "");
        if (prefix.isEmpty()) {
            prefix = "worker";
        }
        final byte[] randomBytes = new byte[5];
        new SecureRandom().nextBytes(randomBytes);
        long bits = 0L;
        for (byte value : randomBytes) {
            bits = (bits << 8) | (value & 0xffL);
        }
        final StringBuilder generated = new StringBuilder(prefix).append('-');
        for (int index = 7; index >= 0; index--) {
            generated.append(BASE32[(int) (bits >>> (index * 5)) & 31]);
        }
        return generated.toString();
    }

    synchronized long getReservedRpcDeclines() {
        return 0L;
    }

    void runBlocking() {
        if (options.shouldWarnRpcCapacity()) {
            LOGGER.warning("RPCs may wait behind long-running Steps; use maxConcurrentTasks >= 2 or set reservedRpcTasks explicitly");
        }
        if (options.shouldWarnFloorRpcCapacity()) {
            LOGGER.warning("RPCs may wait behind long-running Steps; minOpenSlots 1 reserves no RPC slot, so use minOpenSlots >= 2 to reserve one");
        }
        boolean interrupted = false;
        final String limitFailure;
        final PullRegistrationError streamError;
        synchronized (this) {
            startSlotStreams();
            while ((!isStopping || !streams.isEmpty() || busyTasks > 0)
                    && permanentStreamError == null && registrationLimitFailure == null) {
                try {
                    wait();
                } catch (InterruptedException interruption) {
                    interrupted = true;
                    break;
                }
            }
            limitFailure = registrationLimitFailure;
            streamError = permanentStreamError;
        }
        if (limitFailure != null) {
            stop();
            throw new IllegalStateException(limitFailure);
        }
        if (streamError != null) {
            stop();
            throw copyRegistrationError(streamError);
        }
        if (interrupted) {
            stop();
            Thread.currentThread().interrupt();
        }
    }

    synchronized void waitRegistered(final Duration timeout)
            throws InterruptedException, TimeoutException {
        final long deadline = System.nanoTime() + timeout.toNanos();
        while (!hasRegisteredRequiredSlotKinds || registrationLimitFailure != null) {
            if (registrationLimitFailure != null) {
                throw new IllegalStateException(registrationLimitFailure);
            }
            if (initialRegistrationError != null) {
                throw copyRegistrationError(initialRegistrationError);
            }
            if (hasStartReturned) {
                if (startError instanceof RuntimeException) {
                    throw (RuntimeException) startError;
                }
                if (startError instanceof Error) {
                    throw (Error) startError;
                }
                throw new IllegalStateException("pull Worker stopped before registration", startError);
            }
            if (isStopping) {
                throw new IllegalStateException("pull Worker stopped before registration");
            }
            final long remaining = deadline - System.nanoTime();
            if (remaining <= 0) {
                throw new TimeoutException("pull Worker registration timed out");
            }
            TimeUnit.NANOSECONDS.timedWait(this, remaining);
        }
    }

    // Each throw gets a fresh exception so callers never share stack or suppressed state.
    private static PullRegistrationError copyRegistrationError(final PullRegistrationError error) {
        return new PullRegistrationError(error.getQueue(), error.getTaskQueue(), error.getCause());
    }

    synchronized PullRegistrationState registrationState() {
        boolean isReady = hasRegisteredRequiredSlotKinds && registrationLimitFailure == null;
        final List<PullQueueRegistrationState> snapshots = new ArrayList<PullQueueRegistrationState>();
        for (PollWorkerSlotKind slotKind : slotKinds) {
            final SlotRegistration registration = slotRegistrations.get(slotKind);
            final boolean hasRegisteredStream = registration.registeredStreams > 0;
            snapshots.add(new PullQueueRegistrationState(
                    slotKind.name(), taskQueue, hasRegisteredStream, registration.lastPermanentError));
            if (!hasRegisteredStream && registration.lastPermanentError != null) {
                isReady = false;
            }
        }
        return new PullRegistrationState(isReady, snapshots);
    }

    synchronized void finishRegistration(final Throwable failure) {
        hasStartReturned = true;
        startError = failure;
        notifyAll();
    }

    private void recordRegisteredStream(final PullStream stream) {
        final SlotRegistration registration = slotRegistrations.get(stream.slotKind);
        registration.registeredStreams++;
        registration.hasRegistered = true;
        registration.lastPermanentError = null;
        if (!hasRegisteredRequiredSlotKinds) {
            hasRegisteredRequiredSlotKinds = true;
            for (SlotRegistration registeredSlotKind : slotRegistrations.values()) {
                hasRegisteredRequiredSlotKinds &= registeredSlotKind.hasRegistered;
            }
        }
        notifyAll();
    }

    // A soft rejection (F5) is not recorded as a permanent error.
    private void recordStreamResult(final PullStream stream, final Throwable failure, final boolean isSoftRejection) {
        final SlotRegistration registration = slotRegistrations.get(stream.slotKind);
        if (registration == null) {
            // The first acknowledgement removed this slot kind, so it reports no registration state.
            stream.isRegistered = false;
            notifyAll();
            return;
        }
        if (stream.isRegistered) {
            stream.isRegistered = false;
            registration.registeredStreams--;
        }
        if (!isSoftRejection && !stream.shouldRetryRejectedTokenImmediately
                && isPermanentStreamError(stream, failure)) {
            registration.lastPermanentError = failure;
            final PullRegistrationError rejection =
                    new PullRegistrationError(stream.slotKind.name(), taskQueue, failure);
            if (registration.registeredStreams == 0) {
                if (!hasRegisteredRequiredSlotKinds && initialRegistrationError == null) {
                    initialRegistrationError = rejection;
                }
                if (options.isStopOnPermanentStreamRejection() && permanentStreamError == null
                        && registrationLimitFailure == null) {
                    permanentStreamError = rejection;
                }
            }
        }
        notifyAll();
    }

    private boolean isPermanentStreamError(final PullStream stream, final Throwable failure) {
        if (failure != null && failure == stream.taskQueueMismatch) {
            return true;
        }
        final Status.Code code = failure == null ? Status.Code.OK : Status.fromThrowable(failure).getCode();
        return code == Status.Code.UNAUTHENTICATED
                || code == Status.Code.PERMISSION_DENIED
                || code == Status.Code.INVALID_ARGUMENT || code == Status.Code.UNIMPLEMENTED
                || code == Status.Code.RESOURCE_EXHAUSTED;
    }

    void stop() {
        boolean interrupted = false;
        synchronized (this) {
            if (!isStopping) {
                isStopping = true;
                stopDeadlineNanos = System.nanoTime() + options.getStopDrainTimeout().toNanos();
                finalOutputFlushDeadlineNanos = stopDeadlineNanos + FINAL_OUTPUT_FLUSH_GRACE.toNanos();
            }
            final long deadline = stopDeadlineNanos;
            stopShrinkWindow();
            notifyAll();
            for (PullStream stream : new ArrayList<PullStream>(streams)) {
                stream.isDraining = true;
                if (!stream.isBusy) {
                    stream.halfClose();
                }
            }
            while ((!streams.isEmpty() || busyTasks > 0) && System.nanoTime() < deadline) {
                interrupted |= awaitUntil(deadline);
            }
            if (!streams.isEmpty() || busyTasks > 0) {
                for (PullStream stream : new ArrayList<PullStream>(streams)) {
                    if (stream.isFlushingFinalOutput()) {
                        if (!stream.isBusy) {
                            stream.halfClose();
                        }
                    } else {
                        stream.resetForStopTimeout();
                    }
                }
                final long flushDeadline = finalOutputFlushDeadlineNanos;
                while ((!streams.isEmpty() || busyTasks > 0) && System.nanoTime() < flushDeadline) {
                    interrupted |= awaitUntil(flushDeadline);
                }
                for (PullStream stream : new ArrayList<PullStream>(streams)) {
                    stream.resetForStopTimeout();
                }
                while (busyTasks > 0 || !streams.isEmpty()) {
                    try {
                        wait();
                    } catch (InterruptedException interruption) {
                        interrupted = true;
                    }
                }
            }
        }
        scheduler.shutdownNow();
        streamWriters.shutdownNow();
        if (interrupted) {
            Thread.currentThread().interrupt();
        }
    }

    private boolean awaitUntil(final long deadline) {
        final long remaining = Math.max(1L, deadline - System.nanoTime());
        try {
            TimeUnit.NANOSECONDS.timedWait(this, remaining);
            return false;
        } catch (InterruptedException interruption) {
            return true;
        }
    }

    synchronized PullSlotStreamState slotStreamState() {
        return new PullSlotStreamState(isAdaptive, stepSlotOpen(),
                stepPhaseCounts[SlotPhase.REGISTERING.ordinal()], stepPhaseCounts[SlotPhase.BUSY.ordinal()],
                stepPhaseCounts[SlotPhase.IDLE.ordinal()], orphanedStepHandlers, orphanedRpcOnlyHandlers,
                effectiveFloor, effectiveCeiling, growthTarget, totalCapacity, rpcOnlySlotOpen(), rpcOnlySlots,
                streamsGrown, streamsShrunk, growRejectedAtCap, shrinkRaceDrops);
    }

    // Opens the startup streams and starts the shrink window of an adaptive Worker.
    private void startSlotStreams() {
        if (isStopping) {
            return;
        }
        restartShrinkWindow();
        if (isAdaptive || !options.isMaxConcurrentTasksExplicit()) {
            maybeReopenSlotStreams(null);
            return;
        }
        // As before adaptive streams, every Register is enqueued before this monitor section ends.
        final List<PullStream> batch = new ArrayList<PullStream>();
        startupBatch = batch;
        maybeReopenSlotStreams(null);
        startupBatch = null;
        // The old startup order: every RPC_ONLY stream, then every STEP_AND_RPC stream.
        for (PollWorkerSlotKind slotKind : new PollWorkerSlotKind[] {
                PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY,
                PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC}) {
            for (PullStream stream : batch) {
                if (stream.slotKind == slotKind) {
                    stream.connect();
                }
            }
        }
    }

    // Applies D4 and F6 at the first acknowledgement; it returns a start failure, or null.
    private String resolveSlotCeilingAtFirstAcknowledgement(final long limit) {
        final boolean hasLimit = limit != 0;
        if (!isAdaptive && options.isMaxConcurrentTasksExplicit()) {
            if (hasLimit && maxConcurrentTasks > limit) {
                return "maxConcurrentTasks " + maxConcurrentTasks
                        + " exceeds Server setting matching.maxConcurrentTasksCap " + limit
                        + "; lower maxConcurrentTasks or raise the Server setting";
            }
            return null;
        }
        int ceiling = configuredMaxConcurrentTasks;
        if (hasLimit && ceiling > limit) {
            ceiling = (int) limit;
            LOGGER.warning("clamped maxConcurrentTasks " + configuredMaxConcurrentTasks
                    + " to the Server matching.maxConcurrentTasksCap " + limit
                    + " (explicit maxConcurrentTasks: " + options.isMaxConcurrentTasksExplicit() + ")");
        }
        final int resolvedMinOpenSlots = options.isMinOpenSlotsExplicit() ? minOpenSlots : ceiling;
        final int resolvedReservedRpcTasks = options.isReservedRpcTasksExplicit() ? reservedRpcTasks
                : PullWorkerOptions.defaultReservedRpcTasks(ceiling, resolvedMinOpenSlots);
        if (resolvedReservedRpcTasks >= ceiling) {
            return "reservedRpcTasks " + resolvedReservedRpcTasks + " must be less than the slot ceiling " + ceiling
                    + ", which is maxConcurrentTasks " + configuredMaxConcurrentTasks
                    + " limited by Server matching.maxConcurrentTasksCap " + limit;
        }
        if (resolvedMinOpenSlots > ceiling) {
            return "minOpenSlots " + resolvedMinOpenSlots + " exceeds the slot ceiling " + ceiling
                    + ", which is maxConcurrentTasks " + configuredMaxConcurrentTasks
                    + " limited by Server matching.maxConcurrentTasksCap " + limit;
        }
        maxConcurrentTasks = ceiling;
        minOpenSlots = resolvedMinOpenSlots;
        reservedRpcTasks = resolvedReservedRpcTasks;
        stepFloor = resolvedMinOpenSlots - resolvedReservedRpcTasks;
        totalCapacity = ceiling;
        if (resolvedReservedRpcTasks == 0) {
            slotKinds.remove(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY);
            slotRegistrations.remove(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY);
        }
        return null;
    }

    // The registered event of the state machine.
    private void recordSlotRegistered(final PullStream stream, final long limit) {
        stream.slot.hasReceivedRegistered = true;
        if (stream.slot.phase == SlotPhase.REGISTERING) {
            setSlotPhase(stream, SlotPhase.IDLE);
        }
        if (isAdaptive) {
            totalCapacity = limit != 0 && limit < maxConcurrentTasks ? (int) limit : maxConcurrentTasks;
        }
        int nextRpcOnlySlots = reservedRpcTasks;
        if (nextRpcOnlySlots + 1 > totalCapacity) {
            nextRpcOnlySlots = Math.max(0, totalCapacity - 1);
        }
        if (nextRpcOnlySlots < rpcOnlySlots) {
            LOGGER.warning("Server matching.maxConcurrentTasksCap " + limit + " leaves " + nextRpcOnlySlots
                    + " RPC-only slots, fewer than reservedRpcTasks " + reservedRpcTasks);
        }
        rpcOnlySlots = nextRpcOnlySlots;
        closeSurplusSlotStreams();
        recomputeSlotCapacity();
        maybeReopenSlotStreams(null);
        if (stepPhaseCounts[SlotPhase.REGISTERING.ordinal()] == 0) {
            settleGrowthBatch();
        }
    }

    // The task_frame event.
    private void recordSlotTaskStarted(final PullStream stream) {
        if (stream.slot.phase == SlotPhase.UNTRACKED) {
            return;
        }
        setSlotPhase(stream, SlotPhase.BUSY);
        if (stream.slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) {
            return;
        }
        peakBusy = Math.max(peakBusy, stepPhaseCounts[SlotPhase.BUSY.ordinal()]);
        maybeGrowSlotStreams();
    }

    // Counts a task dropped on a stream that the Worker chose to close.
    private void recordSlotTaskDropped(final PullStream stream) {
        if (stream.slot.isClosedOnPurpose && !isStopping) {
            shrinkRaceDrops++;
        }
    }

    // Keeps a stream that the Server asked to reconnect out of the idle set.
    private void recordSlotReconnectRequested(final PullStream stream) {
        if (stream.slot.phase == SlotPhase.IDLE) {
            setSlotPhase(stream, SlotPhase.RECONNECTING);
        }
    }

    // final_frame_sent, or orphaned_handler_returned after the stream ended.
    // It reports whether the stream must half-close because it is surplus.
    private boolean recordSlotTaskCompleted(final PullStream stream, final boolean isDraining) {
        if (stream.slot.isOrphaned) {
            stream.slot.isOrphaned = false;
            if (stream.slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) {
                orphanedRpcOnlyHandlers--;
            } else {
                orphanedStepHandlers--;
            }
            recomputeSlotCapacity();
            maybeReopenSlotStreams(reopenCandidate(stream));
            return false;
        }
        if (stream.slot.phase != SlotPhase.BUSY) {
            return false;
        }
        if (isDraining) {
            setSlotPhase(stream, SlotPhase.RECONNECTING);
            return false;
        }
        setSlotPhase(stream, SlotPhase.IDLE);
        final boolean isSurplus = stream.slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
                ? rpcOnlySlotUsage() > rpcOnlySlots
                : stepSlotUsage() > Math.max(0, totalCapacity - rpcOnlySlots);
        if (!isSurplus) {
            return false;
        }
        halfCloseSlotStream(stream, false);
        streamsShrunk++;
        recomputeSlotCapacity();
        maybeReopenSlotStreams(null);
        return true;
    }

    // F5: adaptive only; an unregistered growth stream or post-readiness reconnect.
    private boolean isSoftSlotRejection(final PullStream stream, final Throwable failure) {
        if (!isAdaptive || failure == null || stream.slot.hasReceivedRegistered
                || Status.fromThrowable(failure).getCode() != Status.Code.RESOURCE_EXHAUSTED) {
            return false;
        }
        if (stream.slot.phase != SlotPhase.REGISTERING) {
            return false;
        }
        return stream.slot.isGrowth || stream.slot.isOpenedAfterReadiness;
    }

    // The soft registration_rejected event.
    private void recordSlotRegistrationRejected(final PullStream stream) {
        stream.slot.isSoftRejected = true;
        setSlotPhase(stream, SlotPhase.UNTRACKED);
        if (stream.slot.isGrowth) {
            hasBatchRejection = true;
        }
        growRejectedAtCap++;
        isReopenDeferred = true;
        recomputeSlotCapacity();
        if (stepPhaseCounts[SlotPhase.REGISTERING.ordinal()] == 0) {
            settleGrowthBatch();
        }
    }

    // stream_ended; every end of an attempt passes here once.
    private void recordSlotStreamEnded(final PullStream stream) {
        if (stream.slot.hasEnded) {
            return;
        }
        stream.slot.hasEnded = true;
        if (stream.slot.phase != SlotPhase.UNTRACKED) {
            if (stream.slot.phase == SlotPhase.BUSY) {
                stream.slot.isOrphaned = true;
                if (stream.slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) {
                    orphanedRpcOnlyHandlers++;
                } else {
                    orphanedStepHandlers++;
                }
            }
            setSlotPhase(stream, SlotPhase.UNTRACKED);
        }
        if (stream.slot.hasReceivedRegistered) {
            isReopenDeferred = false;
        }
        recomputeSlotCapacity();
        maybeReopenSlotStreams(stream.slot.isOrphaned ? null : reopenCandidate(stream));
        maybeGrowSlotStreams();
        if (stepPhaseCounts[SlotPhase.REGISTERING.ordinal()] == 0) {
            settleGrowthBatch();
        }
    }

    // Ends an attempt whose runner exits before the attempt ends.
    private void abandonSlotAttempt(final PullStream stream) {
        if (stream.slot.phase == SlotPhase.UNTRACKED || stream.slot.hasEnded) {
            return;
        }
        stream.slot.isClosedOnPurpose = true;
        recordSlotStreamEnded(stream);
    }

    // Runs the every-W step of the state machine for one window generation.
    private void expireShrinkWindow(final long generation) {
        synchronized (this) {
            if (isStopping || generation != shrinkWindowGeneration) {
                return;
            }
            restartShrinkWindow();
            isGrowthTargetClamped = false;
            hasGrownThisWindow = false;
            isReopenDeferred = false;
            recomputeSlotCapacity();
            maybeReopenSlotStreams(null);
            shrinkSlotStreams();
            peakBusy = stepPhaseCounts[SlotPhase.BUSY.ordinal()];
            maybeGrowSlotStreams();
            notifyAll();
        }
    }

    // Closes half the idle surplus above the window's peak plus headroom.
    private void shrinkSlotStreams() {
        final int windowPeakBusy = peakBusy;
        final int headroom = Math.max(1, (windowPeakBusy + 3) / 4);
        final int target = Math.min(effectiveCeiling, Math.max(effectiveFloor, windowPeakBusy + headroom));
        final int open = stepSlotOpen();
        if (open <= target) {
            return;
        }
        final List<PullStream> idleStreams = idleStepStreamsNewestFirst();
        // The last idle stream stays open, and the floor stays open.
        final int closeCount = Math.min((open - target + 1) / 2,
                Math.min(idleStreams.size() - 1, open - effectiveFloor));
        if (closeCount <= 0) {
            return;
        }
        for (PullStream stream : idleStreams.subList(0, closeCount)) {
            halfCloseSlotStream(stream, true);
        }
        streamsShrunk += closeCount;
        LOGGER.fine("shrink pull slot streams from " + open + " to " + (open - closeCount)
                + " (peakBusy " + windowPeakBusy + ")");
        recomputeSlotCapacity();
    }

    // Releases capacity above a lowered cap, RPC-only first; unsent attempts are revoked first.
    private void closeSurplusSlotStreams() {
        for (PollWorkerSlotKind slotKind : new PollWorkerSlotKind[] {
                PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY,
                PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC}) {
            while (isSlotKindOverQuota(slotKind)) {
                final PullStream pending = unsentSlotAttempt(slotKind);
                if (pending != null) {
                    revokeSlotAttempt(pending);
                    continue;
                }
                final PullStream stream = newestIdleStream(slotKind);
                if (stream == null) {
                    break;
                }
                halfCloseSlotStream(stream, true);
                streamsShrunk++;
            }
        }
    }

    // Runs after each backoff and right before the Register; the Server enforces any later cap drop.
    private boolean admitSlotRegistration(final PullStream stream) {
        if (isStopping || registrationLimitFailure != null || stream.slot.phase != SlotPhase.REGISTERING
                || stream.slot.hasEnded) {
            return false;
        }
        if (!isSlotAttemptOverCapacity(stream)) {
            return true;
        }
        revokeSlotAttempt(stream);
        recomputeSlotCapacity();
        maybeReopenSlotStreams(null);
        if (stepPhaseCounts[SlotPhase.REGISTERING.ordinal()] == 0) {
            settleGrowthBatch();
        }
        return false;
    }

    // Checks one registering attempt, which the totals already count.
    private boolean isSlotAttemptOverCapacity(final PullStream stream) {
        if (stepSlotUsage() + rpcOnlySlotUsage() > totalCapacity) {
            return true;
        }
        if (stream.slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) {
            return rpcOnlySlotUsage() > rpcOnlySlots;
        }
        // F5 gives the first Step-and-RPC stream any free slot, so only later ones need the Step quota.
        return stepSlotOpen() > 1 && stepSlotUsage() > effectiveCeiling;
    }

    // Surplus after a lowered cap: RPC-only above R_eff, Step-and-RPC above C - R_eff.
    private boolean isSlotKindOverQuota(final PollWorkerSlotKind slotKind) {
        if (slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY) {
            return rpcOnlySlotUsage() > rpcOnlySlots;
        }
        return stepSlotUsage() > Math.max(0, totalCapacity - rpcOnlySlots);
    }

    // Takes back an attempt whose Register has not been queued; its runner then exits.
    private void revokeSlotAttempt(final PullStream stream) {
        stream.slot.isClosedOnPurpose = true;
        stream.slot.hasEnded = true;
        setSlotPhase(stream, SlotPhase.UNTRACKED);
    }

    // Runs when no stream registers; a rejected batch clamps G until the window ends.
    private void settleGrowthBatch() {
        if (hasBatchRejection) {
            hasBatchRejection = false;
            growthTarget = Math.min(effectiveCeiling, stepSlotUsage());
            isGrowthTargetClamped = true;
            LOGGER.info("the Server rejected pull slot stream growth at its stream limit; holding growth"
                    + " until the next shrink window (growthTarget " + growthTarget
                    + ", effectiveCeiling " + effectiveCeiling + ")");
        }
        maybeGrowSlotStreams();
    }

    // Doubles the open streams when a hand-out took the last idle one.
    private void maybeGrowSlotStreams() {
        if (!isAdaptive || isStopping || registrationLimitFailure != null || isReopenDeferred) {
            return;
        }
        if (isGrowthTargetClamped && hasGrownThisWindow) {
            return;
        }
        final int open = stepSlotOpen();
        final int usage = stepSlotUsage();
        if (stepPhaseCounts[SlotPhase.IDLE.ordinal()] != 0 || stepPhaseCounts[SlotPhase.REGISTERING.ordinal()] != 0
                || usage >= growthTarget) {
            return;
        }
        final int count = Math.min(growthTarget - usage, Math.max(1, open));
        LOGGER.fine("grow pull slot streams from " + open + " to " + (open + count) + " (no idle stream)");
        for (int index = 0; index < count; index++) {
            openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, true, null);
        }
        streamsGrown += count;
        hasGrownThisWindow = true;
        restartShrinkWindow();
    }

    // Opens streams within free capacity in F5 order, reusing a parked runner for its backoff.
    private void maybeReopenSlotStreams(final PullStream parked) {
        if (isStopping || registrationLimitFailure != null || isReopenDeferred) {
            return;
        }
        int stepOpen = stepSlotOpen();
        int free = Math.max(0, totalCapacity - (stepSlotUsage() + rpcOnlySlotUsage()));
        if (stepOpen == 0 && free > 0) {
            openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, false, parked);
            stepOpen++;
            free--;
        }
        if (isCapProbePending()) {
            return;
        }
        if (hasCheckedRegistrationLimit || !isAdaptive) {
            final int rpcOnlyCount = Math.min(free, Math.max(0, rpcOnlySlots - rpcOnlySlotUsage()));
            for (int index = 0; index < rpcOnlyCount; index++) {
                openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY, false, parked);
            }
            free -= rpcOnlyCount;
        }
        final int stepCount = Math.min(free,
                Math.max(0, Math.min(effectiveFloor, effectiveCeiling - orphanedStepHandlers) - stepOpen));
        for (int index = 0; index < stepCount; index++) {
            openSlotStream(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC, false, parked);
        }
    }

    // Grants a new attempt to parked, or starts a runner that connects from the timer thread.
    private void openSlotStream(final PollWorkerSlotKind slotKind, final boolean isGrowth, final PullStream parked) {
        final SlotAttempt attempt = new SlotAttempt(isGrowth, hasRegisteredRequiredSlotKinds);
        if (!isGrowth && parked != null && parked.slotKind == slotKind && !parked.slot.isReopenGranted) {
            attempt.isReopenGranted = true;
            parked.slot = attempt;
            setSlotPhase(parked, SlotPhase.REGISTERING);
            return;
        }
        final PullStream stream = new PullStream(slotKind, attempt);
        setSlotPhase(stream, SlotPhase.REGISTERING);
        streams.add(stream);
        if (startupBatch != null) {
            startupBatch.add(stream);
            return;
        }
        stream.scheduleConnect(0L);
    }

    // Takes an idle stream out of the totals and marks it closing; its later end changes no count.
    private void halfCloseSlotStream(final PullStream stream, final boolean shouldHalfCloseNow) {
        setSlotPhase(stream, SlotPhase.UNTRACKED);
        stream.slot.isClosedOnPurpose = true;
        stream.isDraining = true;
        if (shouldHalfCloseNow) {
            stream.halfClose();
        }
    }

    // Starts a new shrink window; an older window's timer then does nothing.
    private void restartShrinkWindow() {
        if (!isAdaptive || isStopping || slotSettings.getShrinkWindow().isZero()
                || slotSettings.getShrinkWindow().isNegative()) {
            return;
        }
        shrinkWindowGeneration++;
        final long generation = shrinkWindowGeneration;
        if (shrinkWindowTimer != null) {
            shrinkWindowTimer.cancel(false);
        }
        try {
            shrinkWindowTimer = scheduler.schedule(() -> expireShrinkWindow(generation),
                    slotSettings.getShrinkWindow().toNanos(), TimeUnit.NANOSECONDS);
        } catch (RejectedExecutionException rejected) {
            LOGGER.log(Level.WARNING, "cannot schedule the pull slot shrink window", rejected);
        }
    }

    // Stops the shrink window timer at stop.
    private void stopShrinkWindow() {
        shrinkWindowGeneration++;
        if (shrinkWindowTimer != null) {
            shrinkWindowTimer.cancel(false);
            shrinkWindowTimer = null;
        }
    }

    // Moves an attempt between phases and keeps the per-phase totals.
    private void setSlotPhase(final PullStream stream, final SlotPhase phase) {
        final int[] counts = stream.slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
                ? rpcOnlyPhaseCounts : stepPhaseCounts;
        if (stream.slot.phase != SlotPhase.UNTRACKED) {
            counts[stream.slot.phase.ordinal()]--;
        }
        stream.slot.phase = phase;
        if (phase != SlotPhase.UNTRACKED) {
            counts[phase.ordinal()]++;
        }
        if (phase == SlotPhase.IDLE) {
            nextIdleSequence++;
            stream.slot.idleSequence = nextIdleSequence;
        }
    }

    // Derives E, L_eff, and G; RPC-only overflow takes Step capacity.
    private void recomputeSlotCapacity() {
        final int rpcOnlyReserve = Math.max(rpcOnlySlots, rpcOnlySlotUsage());
        effectiveCeiling = Math.max(0, totalCapacity - rpcOnlyReserve);
        effectiveFloor = Math.max(0, Math.min(stepFloor, effectiveCeiling));
        growthTarget = isGrowthTargetClamped ? Math.min(growthTarget, effectiveCeiling) : effectiveCeiling;
    }

    // Returns stream when its runner may serve the next attempt of its kind.
    private PullStream reopenCandidate(final PullStream stream) {
        if (stream.slot.isClosedOnPurpose || stream.slot.isSoftRejected || stream.slot.isReopenGranted) {
            return null;
        }
        return stream;
    }

    // Lists idle Step-and-RPC streams, the most recently idle first.
    private List<PullStream> idleStepStreamsNewestFirst() {
        final List<PullStream> idleStreams = new ArrayList<PullStream>();
        for (PullStream stream : streams) {
            if (stream.slotKind != PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
                    && stream.slot.phase == SlotPhase.IDLE) {
                idleStreams.add(stream);
            }
        }
        Collections.sort(idleStreams, (left, right) -> Long.compare(right.slot.idleSequence, left.slot.idleSequence));
        return idleStreams;
    }

    // Returns a registering attempt of the kind whose Register has not been queued, or null.
    private PullStream unsentSlotAttempt(final PollWorkerSlotKind slotKind) {
        for (PullStream stream : streams) {
            if (stream.slotKind == slotKind && stream.slot.phase == SlotPhase.REGISTERING
                    && !stream.slot.isRegisterSent) {
                return stream;
            }
        }
        return null;
    }

    private PullStream newestIdleStream(final PollWorkerSlotKind slotKind) {
        PullStream newest = null;
        for (PullStream stream : streams) {
            if (stream.slotKind == slotKind && stream.slot.phase == SlotPhase.IDLE
                    && (newest == null || stream.slot.idleSequence > newest.slot.idleSequence)) {
                newest = stream;
            }
        }
        return newest;
    }

    // Reports F4: a fixed Worker with an unset ceiling opens one stream until the first acknowledgement.
    private boolean isCapProbePending() {
        return !isAdaptive && !options.isMaxConcurrentTasksExplicit() && !hasCheckedRegistrationLimit;
    }

    // Returns S.
    private int stepSlotOpen() {
        return openCount(stepPhaseCounts);
    }

    // Returns S + H.
    private int stepSlotUsage() {
        return stepSlotOpen() + orphanedStepHandlers;
    }

    // Returns Sr.
    private int rpcOnlySlotOpen() {
        return openCount(rpcOnlyPhaseCounts);
    }

    // Returns Sr + Hr.
    private int rpcOnlySlotUsage() {
        return rpcOnlySlotOpen() + orphanedRpcOnlyHandlers;
    }

    private static int openCount(final int[] counts) {
        return counts[SlotPhase.REGISTERING.ordinal()] + counts[SlotPhase.IDLE.ordinal()]
                + counts[SlotPhase.BUSY.ordinal()] + counts[SlotPhase.RECONNECTING.ordinal()];
    }

    private long retryDelay(final long backoff) {
        return backoff + (long) (jitter.nextDouble() * backoff / 2.0);
    }

    private final class PullStream implements StreamObserver<PollWorkerTaskResponse> {
        private final PollWorkerSlotKind slotKind;
        private volatile AtomicReference<String> callToken;
        private volatile int openGeneration;
        private PullConnection connection;
        private PollWorkerTask pendingTask;
        private boolean isPendingTaskCancelled;
        private boolean hasUnconfirmedFinalOutput;
        private Context.CancellableContext taskContext;
        private ScheduledFuture<?> deadlineTimer;
        private ScheduledFuture<?> keepaliveTimer;
        private ScheduledFuture<?> closeTimer;
        private Thread handlerThread;
        private Throwable closeFailure;
        private Throwable taskQueueMismatch;
        private String taskID;
        private volatile Status.Code cancellationStatus;
        private long backoffMillis = 100L;
        private boolean isBusy;
        private boolean isFinalFrameSent;
        private boolean isDraining;
        private boolean isHalfClosed;
        private boolean isClosed;
        private boolean isRegistered;
        private boolean hasRetriedRejectedToken;
        private boolean shouldRetryRejectedTokenImmediately;
        private boolean wasSoftRejected;
        private boolean hasExited;
        // The current attempt in the slot stream state machine; the PullWorker monitor guards it.
        private SlotAttempt slot;

        // A new runner has no call yet, so it starts closed until its first connect.
        private PullStream(final PollWorkerSlotKind slotKind, final SlotAttempt slot) {
            this.slotKind = slotKind;
            this.slot = slot;
            this.isClosed = true;
        }

        private void scheduleConnect(final long delayMillis) {
            try {
                scheduler.schedule(this::connect, delayMillis, TimeUnit.MILLISECONDS);
            } catch (RejectedExecutionException rejected) {
                // The timer stops only after stop began, so the runner has nothing left to open.
                LOGGER.log(Level.FINE, "pull slot stream connect after stop", rejected);
                exit();
            }
        }

        // Admits the attempt after its backoff, then opens the call; the test hook runs between them.
        private void connect() {
            synchronized (PullWorker.this) {
                if (!admitSlotRegistration(this)) {
                    exit();
                    return;
                }
                final PullSlotSettings.RegisterSendHook hook = slotSettings.getBeforeRegisterSendForTest();
                if (hook == null) {
                    open();
                    return;
                }
                try {
                    streamWriters.execute(() -> {
                        hook.beforeRegisterSend(slotKind);
                        synchronized (PullWorker.this) {
                            open();
                        }
                    });
                } catch (RejectedExecutionException rejected) {
                    LOGGER.log(Level.FINE, "pull slot stream connect after stop", rejected);
                    exit();
                }
            }
        }

        // Ends this runner; only the reopen rule starts another one.
        private void exit() {
            if (hasExited) {
                return;
            }
            hasExited = true;
            cancelTask(Status.Code.CANCELLED);
            abandonSlotAttempt(this);
            streams.remove(this);
            maybeReopenSlotStreams(null);
            PullWorker.this.notifyAll();
        }

        private void open() {
            // The cap that another stream reported may have changed since this attempt was admitted.
            if (!admitSlotRegistration(this)) {
                exit();
                return;
            }
            final int generation = ++openGeneration;
            final AtomicReference<String> openedToken = new AtomicReference<String>();
            callToken = openedToken;
            isClosed = false;
            isHalfClosed = false;
            isRegistered = false;
            pendingTask = null;
            isPendingTaskCancelled = false;
            hasUnconfirmedFinalOutput = false;
            isFinalFrameSent = false;
            closeFailure = null;
            taskQueueMismatch = null;
            final PullConnection opened = new PullConnection(Context.ROOT.withCancellation());
            connection = opened;
            try {
                final StreamObserver<PollWorkerTaskResponse> observer =
                        new StreamObserver<PollWorkerTaskResponse>() {
                            @Override
                            public void onNext(final PollWorkerTaskResponse response) {
                                synchronized (PullWorker.this) {
                                    if (generation == openGeneration) {
                                        PullStream.this.onNext(response);
                                    }
                                }
                            }

                            @Override
                            public void onError(final Throwable failure) {
                                close(failure, openedToken.get(), generation);
                            }

                            @Override
                            public void onCompleted() {
                                close(null, null, generation);
                            }
                        };
                // Cancelling this context is grpc-java's thread-safe reset; it sends RST_STREAM.
                opened.callContext.run(() -> GrpcCredential.withTokenCapture(openedToken).run(() ->
                        opened.requests = taskService.pollWorkerTask(observer)));
                final PullSlotSettings.RequestObserverWrapper wrapper =
                        slotSettings.getRequestObserverWrapperForTest();
                if (wrapper != null) {
                    opened.requests = wrapper.wrap(opened.requests, opened.callContext, slotKind);
                }
                // The writer thread sends the Register outside this monitor; from here on it is not revocable.
                slot.isRegisterSent = true;
                send(PollWorkerTaskRequest.newBuilder()
                        .setRegister(PollWorkerTaskRegister.newBuilder()
                                .setSupportsReservedSlotDispatch(true)
                                .setSlotKind(slotKind)
                                .setAppWorkerId(appWorkerID)
                                .setTaskQueue(taskQueue)
                                .addAllFlowTypes(flowTypes)
                                .setSdkName("java")
                                .setSdkVersion(sdkVersion)
                                .build())
                        .build());
            } catch (RuntimeException failure) {
                close(failure);
            }
        }

        @Override
        public void onNext(final PollWorkerTaskResponse response) {
            synchronized (PullWorker.this) {
                if (isClosed) {
                    return;
                }
                if (!isRegistered) {
                    if (registrationLimitFailure != null || isStopping || isDraining || isHalfClosed) {
                        // This stream takes no more work; a task dropped here was never started.
                        isDraining = true;
                        halfClose();
                    } else if (response.hasReconnect()) {
                        isDraining = true;
                        halfClose();
                    } else if (response.hasRegistered()) {
                        final String registeredTaskQueue = response.getRegistered().getTaskQueue();
                        if (!registeredTaskQueue.equals(taskQueue)) {
                            rejectTaskQueueMismatch(registeredTaskQueue);
                            return;
                        }
                        if (!isRegistrationWithinServerLimit(response)) {
                            isDraining = true;
                            halfClose();
                            PullWorker.this.notifyAll();
                            return;
                        }
                        isRegistered = true;
                        recordRegisteredStream(this);
                        backoffMillis = 100L;
                        hasRetriedRejectedToken = false;
                        recordSlotRegistered(this, Integer.toUnsignedLong(
                                response.getRegistered().getMaxConcurrentTasksPerAppWorker()));
                    } else {
                        reset("Worker task stream did not register");
                    }
                    return;
                }
                if (response.hasTask()) {
                    // Dex assigns a task only after it has received the previous final output.
                    hasUnconfirmedFinalOutput = false;
                    startTask(response.getTask());
                } else if (response.hasCancelTask()) {
                    final String cancelledTaskID = response.getCancelTask().getTaskId();
                    if (cancelledTaskID.equals(taskID)) {
                        cancelTask(Status.Code.CANCELLED);
                    } else if (pendingTask != null && cancelledTaskID.equals(pendingTask.getTaskId())) {
                        isPendingTaskCancelled = true;
                    }
                } else if (response.hasPing()) {
                    send(PollWorkerTaskRequest.newBuilder()
                            .setPong(PollWorkerTaskPong.getDefaultInstance()).build());
                } else if (response.hasReconnect()) {
                    isDraining = true;
                    recordSlotReconnectRequested(this);
                    if (!isBusy) {
                        halfClose();
                    } else {
                        maybeReopenSlotStreams(null);
                    }
                }
            }
        }

        // A Server without named task queues silently registers the default task queue.
        private void rejectTaskQueueMismatch(final String registeredTaskQueue) {
            taskQueueMismatch = Status.FAILED_PRECONDITION.withDescription(
                    "Server registered task queue \"" + registeredTaskQueue + "\" instead of \""
                            + taskQueue + "\"; it may not support named task queues")
                    .asRuntimeException();
            shouldRetryRejectedTokenImmediately = false;
            recordStreamResult(this, taskQueueMismatch, false);
            // Half-close so Dex returns tasks sent after Registered without consuming an attempt.
            halfClose();
        }

        // Checks the Server's per-Worker slot limit on the first registration after start only.
        // Changed in 1.45.4: an unset fixed ceiling and any adaptive ceiling are clamped instead (D4, F6).
        private boolean isRegistrationWithinServerLimit(final PollWorkerTaskResponse response) {
            final long serverLimit = Integer.toUnsignedLong(response.getRegistered()
                    .getMaxConcurrentTasksPerAppWorker());
            final boolean isWithinLimit = serverLimit == 0 || serverLimit >= maxConcurrentTasks;
            if (hasCheckedRegistrationLimit) {
                if (!isWithinLimit && !isAdaptive) {
                    LOGGER.warning("maxConcurrentTasks " + maxConcurrentTasks
                            + " exceeds the Server setting matching.maxConcurrentTasksCap " + serverLimit
                            + " reported on reconnect; the Worker keeps running and the Server may reject"
                            + " extra slot streams");
                }
                return true;
            }
            hasCheckedRegistrationLimit = true;
            final String failure = resolveSlotCeilingAtFirstAcknowledgement(serverLimit);
            if (failure != null) {
                registrationLimitFailure = failure;
                stopShrinkWindow();
                return false;
            }
            return true;
        }

        private void startTask(final PollWorkerTask task) {
            if (isStopping || isDraining || isHalfClosed || registrationLimitFailure != null) {
                recordSlotTaskDropped(this);
                return;
            }
            if (isBusy && isFinalFrameSent && pendingTask == null) {
                pendingTask = task;
                return;
            }
            if (isBusy) {
                reset("Server assigned two tasks to one reserved slot");
                return;
            }
            final long deadlineDelayMillis;
            try {
                deadlineDelayMillis = deadlineDelayMillis(task);
            } catch (DateTimeException | ArithmeticException malformed) {
                LOGGER.log(Level.WARNING, "Worker task has a malformed deadline", malformed);
                reset("Worker task has a malformed deadline");
                return;
            }
            if (task.getRequestCase() == PollWorkerTask.RequestCase.REQUEST_NOT_SET) {
                reset("Worker task has no request");
                return;
            }
            if (task.getTaskId().isEmpty()) {
                reset("Worker task has no task ID");
                return;
            }
            final PollWorkerTaskRejectReason unknownReason = unknownTaskReason(task);
            if (unknownReason != PollWorkerTaskRejectReason.POLL_WORKER_TASK_REJECT_REASON_UNSPECIFIED) {
                reject(task.getTaskId(), unknownReason);
                return;
            }
            isBusy = true;
            isFinalFrameSent = false;
            busyTasks++;
            taskID = task.getTaskId();
            taskContext = Context.current().withCancellation();
            cancellationStatus = null;
            try {
                scheduleTaskTimers(deadlineDelayMillis);
                handlers.execute(() -> runTask(task));
            } catch (RuntimeException admissionFailure) {
                LOGGER.log(Level.WARNING, "cannot start Worker task", admissionFailure);
                reset("Worker cannot start task");
                finishTask();
                return;
            }
            recordSlotTaskStarted(this);
        }

        // Returns Long.MAX_VALUE without a deadline; throws for an unrepresentable deadline.
        private long deadlineDelayMillis(final PollWorkerTask task) {
            if (!task.hasDeadline()) {
                return Long.MAX_VALUE;
            }
            final Instant deadline = Instant.ofEpochSecond(
                    task.getDeadline().getSeconds(), task.getDeadline().getNanos());
            return Duration.between(Instant.now(), deadline).toMillis();
        }

        private void scheduleTaskTimers(final long deadlineDelayMillis) {
            keepaliveTimer = scheduler.scheduleAtFixedRate(() -> {
                synchronized (PullWorker.this) {
                    if (isBusy && !isFinalFrameSent && !isHalfClosed && !isClosed) {
                        send(PollWorkerTaskRequest.newBuilder()
                                .setPong(PollWorkerTaskPong.getDefaultInstance()).build());
                    }
                }
            }, BUSY_PULL_STREAM_KEEPALIVE_SECONDS, BUSY_PULL_STREAM_KEEPALIVE_SECONDS, TimeUnit.SECONDS);
            if (deadlineDelayMillis == Long.MAX_VALUE) {
                return;
            }
            if (deadlineDelayMillis <= 0L) {
                cancelTask(Status.Code.DEADLINE_EXCEEDED);
                return;
            }
            final String deadlineTaskID = taskID;
            deadlineTimer = scheduler.schedule(() -> {
                synchronized (PullWorker.this) {
                    if (deadlineTaskID.equals(taskID)) {
                        cancelTask(Status.Code.DEADLINE_EXCEEDED);
                    }
                }
            }, deadlineDelayMillis, TimeUnit.MILLISECONDS);
        }

        private void reject(final String rejectedTaskID, final PollWorkerTaskRejectReason reason) {
            send(PollWorkerTaskRequest.newBuilder()
                    .setReject(PollWorkerTaskReject.newBuilder()
                            .setTaskId(rejectedTaskID)
                            .setReason(reason)
                            .build())
                    .build());
        }

        private void runTask(final PollWorkerTask task) {
            synchronized (PullWorker.this) {
                handlerThread = Thread.currentThread();
            }
            final Context previous = taskContext.attach();
            try {
                if (taskContext.isCancelled()) {
                    throw Status.CANCELLED.asRuntimeException();
                }
                switch (task.getRequestCase()) {
                    case WAIT_FOR:
                        sendWaitFor(task);
                        break;
                    case EXECUTE:
                        sendExecute(task);
                        break;
                    case RPC:
                        sendFrame(PollWorkerTaskFrame.newBuilder()
                                .setRpc(dispatcher.invokeRpc(task.getRpc())).build());
                        break;
                    default:
                        throw new IllegalArgumentException("unsupported Worker task request");
                }
            } catch (Throwable failure) {
                LOGGER.log(Level.SEVERE, "Java pull Worker invocation failed", failure);
                sendFrame(PollWorkerTaskFrame.newBuilder()
                        .setWorkerError(errorMapper.mapFailure(failure, cancellationStatus))
                        .build());
            } finally {
                taskContext.detach(previous);
                synchronized (PullWorker.this) {
                    finishTask();
                }
            }
        }

        private void sendWaitFor(final PollWorkerTask task) throws Throwable {
            final Context.CancellableContext owningContext = taskContext;
            final StepOutputEmitter emitter = new StepOutputEmitter() {
                @Override
                public void emitHeartbeat(final StepMethodHeartbeat heartbeat) {
                    sendStepOutput(PollWorkerTaskFrame.newBuilder()
                            .setWaitFor(InvokeWaitForMethodOutput.newBuilder()
                                    .setHeartbeat(heartbeat).build()).build(), task.getTaskId(), owningContext);
                }

                @Override
                public void emitStreamWrite(final StepStreamWrite streamWrite) {
                    sendStepOutput(PollWorkerTaskFrame.newBuilder()
                            .setWaitFor(InvokeWaitForMethodOutput.newBuilder()
                                    .setStreamWrite(streamWrite).build()).build(), task.getTaskId(), owningContext);
                }
            };
            sendFrame(PollWorkerTaskFrame.newBuilder()
                    .setWaitFor(InvokeWaitForMethodOutput.newBuilder()
                            .setResult(dispatcher.invokeWaitFor(task.getWaitFor(), emitter)).build())
                    .build());
        }

        private void sendExecute(final PollWorkerTask task) throws Throwable {
            final Context.CancellableContext owningContext = taskContext;
            final StepOutputEmitter emitter = new StepOutputEmitter() {
                @Override
                public void emitHeartbeat(final StepMethodHeartbeat heartbeat) {
                    sendStepOutput(PollWorkerTaskFrame.newBuilder()
                            .setExecute(InvokeExecuteMethodOutput.newBuilder()
                                    .setHeartbeat(heartbeat).build()).build(), task.getTaskId(), owningContext);
                }

                @Override
                public void emitStreamWrite(final StepStreamWrite streamWrite) {
                    sendStepOutput(PollWorkerTaskFrame.newBuilder()
                            .setExecute(InvokeExecuteMethodOutput.newBuilder()
                                    .setStreamWrite(streamWrite).build()).build(), task.getTaskId(), owningContext);
                }
            };
            sendFrame(PollWorkerTaskFrame.newBuilder()
                    .setExecute(InvokeExecuteMethodOutput.newBuilder()
                            .setResult(dispatcher.invokeExecute(task.getExecute(), emitter)).build())
                    .build());
        }

        private void sendStepOutput(final PollWorkerTaskFrame frame, final String owningTaskID,
                final Context.CancellableContext owningContext) {
            synchronized (PullWorker.this) {
                if (!isBusy || isFinalFrameSent || owningContext != taskContext
                        || !owningTaskID.equals(taskID) || isHalfClosed || isClosed) {
                    throw Status.CANCELLED.withDescription("Worker task is no longer active")
                            .asRuntimeException();
                }
                if (cancellationStatus != null) {
                    throw Status.fromCode(cancellationStatus).asRuntimeException();
                }
                sendFrame(frame);
            }
        }

        private void sendFrame(final PollWorkerTaskFrame frame) {
            synchronized (PullWorker.this) {
                if (frame.hasRpc() || frame.hasWorkerError()
                        || frame.hasWaitFor() && frame.getWaitFor().hasResult()
                        || frame.hasExecute() && frame.getExecute().hasResult()) {
                    stopKeepalive();
                    isFinalFrameSent = true;
                    hasUnconfirmedFinalOutput = true;
                }
                send(PollWorkerTaskRequest.newBuilder()
                        .setTaskFrame(frame.toBuilder().setTaskId(taskID)).build());
            }
        }

        private void stopKeepalive() {
            if (keepaliveTimer != null) {
                keepaliveTimer.cancel(false);
                keepaliveTimer = null;
            }
        }

        private void send(final PollWorkerTaskRequest request) {
            synchronized (PullWorker.this) {
                if (!isHalfClosed && !isClosed && connection != null && connection.requests != null
                        && (!request.hasPong() || !isFinalFrameSent)) {
                    enqueueWrite(request);
                }
            }
        }

        // A null request half-closes; each connection has its own ordered writer.
        private void enqueueWrite(final PollWorkerTaskRequest request) {
            final PullConnection target = connection;
            target.pendingWrites.add(new PendingWrite(request));
            if (target.isWriterScheduled) {
                return;
            }
            target.isWriterScheduled = true;
            try {
                streamWriters.execute(() -> drainWrites(target));
            } catch (RejectedExecutionException rejected) {
                target.isWriterScheduled = false;
                reset("Worker stream writer stopped");
            }
        }

        // Fails here, before gRPC serializes the frame, so the SDK resets the stream itself.
        private void requireSerializable(final PollWorkerTaskRequest request) {
            if (request.getSerializedSize() < 0) {
                throw new IllegalStateException("Worker task frame exceeds the protobuf size limit");
            }
        }

        private void drainWrites(final PullConnection target) {
            while (true) {
                final PendingWrite write;
                synchronized (PullWorker.this) {
                    write = target.isCancelled ? null : target.pendingWrites.poll();
                    if (write == null) {
                        target.isWriterScheduled = false;
                        return;
                    }
                }
                // A reset can overtake this write; gRPC then drops it after the RST_STREAM.
                try {
                    if (write.request == null) {
                        target.requests.onCompleted();
                    } else {
                        requireSerializable(write.request);
                        target.requests.onNext(write.request);
                    }
                } catch (RuntimeException failure) {
                    synchronized (PullWorker.this) {
                        if (target == connection && !target.isCancelled && !isClosed) {
                            LOGGER.log(Level.WARNING, "Worker task stream write failed", failure);
                            reset("Worker task stream write failed");
                        }
                    }
                }
            }
        }

        private void cancelConnection(final Throwable cause) {
            if (connection != null && !connection.isCancelled) {
                connection.isCancelled = true;
                connection.pendingWrites.clear();
                connection.callContext.cancel(cause);
            }
        }

        private void cancelTask(final Status.Code code) {
            synchronized (PullWorker.this) {
                if (taskContext != null && cancellationStatus == null) {
                    cancellationStatus = code;
                    taskContext.cancel(Status.fromCode(code).asRuntimeException());
                    if (handlerThread != null) {
                        handlerThread.interrupt();
                    }
                }
            }
        }

        private void finishTask() {
            final boolean hasSentFinalOutput = isFinalFrameSent;
            stopKeepalive();
            if (deadlineTimer != null) {
                deadlineTimer.cancel(false);
                deadlineTimer = null;
            }
            if (taskContext != null) {
                taskContext.cancel(null);
                taskContext = null;
            }
            handlerThread = null;
            taskID = null;
            isBusy = false;
            isFinalFrameSent = false;
            busyTasks--;
            // The slot leaves BUSY before any reset below, so the stream's end never counts a returned handler.
            final boolean isSurplus = recordSlotTaskCompleted(this, isDraining && !isClosed);
            if (isClosed) {
                retry(closeFailure);
            } else if (!hasSentFinalOutput) {
                // A clean half-close would tell Dex the task never ran.
                reset("Worker task ended without a final output");
            } else if (isDraining || isStopping || isSurplus) {
                halfClose();
            } else if (pendingTask != null) {
                final PollWorkerTask nextTask = pendingTask;
                final boolean shouldCancelNextTask = isPendingTaskCancelled;
                pendingTask = null;
                isPendingTaskCancelled = false;
                startTask(nextTask);
                if (shouldCancelNextTask && nextTask.getTaskId().equals(taskID)) {
                    cancelTask(Status.Code.CANCELLED);
                }
            }
            PullWorker.this.notifyAll();
        }

        private boolean isFlushingFinalOutput() {
            return !isClosed && hasUnconfirmedFinalOutput && (!isBusy || isFinalFrameSent);
        }

        private void reset(final String reason) {
            final RuntimeException failure = Status.INTERNAL.withDescription(reason).asRuntimeException();
            cancelConnection(failure);
            close(failure);
        }

        private void resetForStopTimeout() {
            cancelTask(Status.Code.CANCELLED);
            if (!isClosed) {
                final RuntimeException cancellation = Status.CANCELLED
                        .withDescription("Worker stop timeout").asRuntimeException();
                cancelConnection(cancellation);
                close(cancellation);
            }
        }

        private void halfClose() {
            stopKeepalive();
            if (isClosed) {
                if (!isBusy && isStopping) {
                    exit();
                }
                return;
            }
            if (isHalfClosed) {
                if (isStopping) {
                    scheduleCloseFallback();
                }
                return;
            }
            isHalfClosed = true;
            enqueueWrite(null);
            scheduleCloseFallback();
        }

        private void scheduleCloseFallback() {
            if (closeTimer != null) {
                closeTimer.cancel(false);
            }
            final long waitNanos = isStopping
                    ? Math.max(1L, (hasUnconfirmedFinalOutput ? finalOutputFlushDeadlineNanos
                            : stopDeadlineNanos) - System.nanoTime())
                    : options.getStopDrainTimeout().toNanos()
                            + (hasUnconfirmedFinalOutput ? FINAL_OUTPUT_FLUSH_GRACE.toNanos() : 0L);
            closeTimer = scheduler.schedule(() -> {
                synchronized (PullWorker.this) {
                    if (!isClosed) {
                        final RuntimeException timeout = Status.DEADLINE_EXCEEDED
                                .withDescription("Worker stream close timed out")
                                .asRuntimeException();
                        cancelConnection(timeout);
                        close(timeout);
                    }
                }
            }, waitNanos, TimeUnit.NANOSECONDS);
        }

        @Override
        public void onError(final Throwable failure) {
            close(failure);
        }

        @Override
        public void onCompleted() {
            close(null);
        }

        private void close(final Throwable failure) {
            close(failure, callToken == null ? null : callToken.get(), openGeneration);
        }

        private void close(final Throwable reportedFailure, final String rejectedToken,
                final int generation) {
            synchronized (PullWorker.this) {
                if (isClosed || generation != openGeneration) {
                    return;
                }
                final Throwable failure = taskQueueMismatch != null ? taskQueueMismatch : reportedFailure;
                shouldRetryRejectedTokenImmediately = failure != null
                        && Status.fromThrowable(failure).getCode() == Status.Code.UNAUTHENTICATED
                        && credential instanceof RejectedTokenCredential && rejectedToken != null
                        && !hasRetriedRejectedToken;
                if (failure != null
                        && Status.fromThrowable(failure).getCode() == Status.Code.UNAUTHENTICATED) {
                    GrpcCredential.rejectToken(credential, rejectedToken);
                }
                isClosed = true;
                // The call has ended; cancelling releases its context and drops unsent frames.
                cancelConnection(failure == null ? Status.CANCELLED.asRuntimeException() : failure);
                final boolean isSoftRejection = !isStopping && isSoftSlotRejection(this, failure);
                if (taskQueueMismatch == null) {
                    recordStreamResult(this, failure, isSoftRejection);
                }
                if (isSoftRejection) {
                    recordSlotRegistrationRejected(this);
                }
                wasSoftRejected = isSoftRejection;
                recordSlotStreamEnded(this);
                closeFailure = failure;
                if (closeTimer != null) {
                    closeTimer.cancel(false);
                }
                if (isBusy) {
                    cancelTask(Status.Code.CANCELLED);
                    return;
                }
                retry(failure);
            }
        }

        private void retry(final Throwable failure) {
            if (isStopping) {
                exit();
                return;
            }
            if (registrationLimitFailure != null) {
                PullWorker.this.notifyAll();
                return;
            }
            if (wasSoftRejected) {
                // A soft rejection waits for another registered stream to end or for the next shrink window.
                LOGGER.log(Level.FINE, "Worker task stream reached the Server stream limit for slot kind "
                        + slotKind, failure);
                exit();
                return;
            }
            final Status.Code code = failure == null ? Status.Code.OK
                    : Status.fromThrowable(failure).getCode();
            final boolean isPermanent = isPermanentStreamError(this, failure);
            if (code == Status.Code.UNIMPLEMENTED && !hasLoggedUnsupported) {
                hasLoggedUnsupported = true;
                LOGGER.log(Level.SEVERE, "pull Worker polling is unsupported by this Dex server", failure);
            } else if (isPermanent && code != Status.Code.UNIMPLEMENTED) {
                LOGGER.log(Level.SEVERE, "Worker task stream rejected for slot kind " + slotKind, failure);
            } else if (failure != null && code != Status.Code.CANCELLED) {
                LOGGER.log(Level.WARNING, "Worker task stream disconnected for slot kind " + slotKind, failure);
            }
            if (isDraining) {
                backoffMillis = 100L;
            }
            final long delay = shouldRetryRejectedTokenImmediately
                    ? 0L : retryDelay(isPermanent ? 60_000L : backoffMillis);
            if (shouldRetryRejectedTokenImmediately) {
                hasRetriedRejectedToken = true;
            }
            if (isDraining) {
                isDraining = false;
            } else if (!isPermanent) {
                backoffMillis = Math.min(backoffMillis * 2, 5000L);
            }
            // Only the reopen rule continues this runner; a stream closed on purpose never reopens.
            if (!slot.isReopenGranted) {
                exit();
                return;
            }
            slot.isReopenGranted = false;
            scheduleConnect(delay);
            PullWorker.this.notifyAll();
        }
    }

    // Owns one poll call and its ordered writes; fields are guarded by the PullWorker monitor.
    private static final class PullConnection {
        private final Context.CancellableContext callContext;
        private final ArrayDeque<PendingWrite> pendingWrites = new ArrayDeque<PendingWrite>();
        private volatile StreamObserver<PollWorkerTaskRequest> requests;
        private boolean isWriterScheduled;
        private boolean isCancelled;

        private PullConnection(final Context.CancellableContext callContext) {
            this.callContext = callContext;
        }
    }

    private static final class PendingWrite {
        private final PollWorkerTaskRequest request;

        private PendingWrite(final PollWorkerTaskRequest request) {
            this.request = request;
        }
    }
}
