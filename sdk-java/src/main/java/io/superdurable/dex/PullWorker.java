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
import io.grpc.stub.ClientCallStreamObserver;
import io.grpc.stub.ClientResponseObserver;
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
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Random;
import java.util.concurrent.ExecutorService;
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
    private static final char[] BASE32 = "abcdefghijklmnopqrstuvwxyz234567".toCharArray();
    private final WorkerTaskServiceGrpc.WorkerTaskServiceStub taskService;
    private final Credential credential;
    private final WorkerErrorMapper errorMapper;
    private final WorkerDispatcher dispatcher;
    private final ExecutorService handlers;
    private final ScheduledThreadPoolExecutor scheduler;
    private final PullWorkerOptions options;
    private final List<String> flowTypes;
    private final Map<String, Registry.RegisteredFlow> registeredFlows;
    private final List<PollWorkerSlotKind> slotKinds;
    private final List<PullStream> streams = new ArrayList<PullStream>();
    private final String appWorkerID;
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
    private IllegalStateException registrationLimitError;

    private static final class SlotRegistration {
        private int registeredStreams;
        private boolean hasRegistered;
        private Throwable lastPermanentError;
    }
    private boolean isStopping;
    private boolean hasLoggedUnsupported;
    private long stopDeadlineNanos;

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
        if (registered.contains(appWorkerID)) {
            scheduler.shutdownNow();
            throw new IllegalArgumentException("appWorkerID conflicts with Flow type " + appWorkerID);
        }
        this.slotKinds = new ArrayList<PollWorkerSlotKind>();
        slotKinds.add(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC);
        if (options.getReservedRpcTasks() > 0) {
            slotKinds.add(PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY);
        }
        for (PollWorkerSlotKind slotKind : slotKinds) {
            slotRegistrations.put(slotKind, new SlotRegistration());
        }
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
        boolean interrupted = false;
        synchronized (this) {
            reconcile();
            while ((!isStopping || !streams.isEmpty() || busyTasks > 0)
                    && permanentStreamError == null && registrationLimitError == null) {
                try {
                    wait();
                } catch (InterruptedException interruption) {
                    interrupted = true;
                    break;
                }
            }
        }
        if (registrationLimitError != null) {
            stop();
            throw registrationLimitError;
        }
        if (permanentStreamError != null) {
            stop();
            throw permanentStreamError;
        }
        if (interrupted) {
            stop();
            Thread.currentThread().interrupt();
        }
    }

    synchronized void waitRegistered(final Duration timeout)
            throws InterruptedException, TimeoutException {
        final long deadline = System.nanoTime() + timeout.toNanos();
        while (!hasRegisteredRequiredSlotKinds) {
            if (registrationLimitError != null) {
                throw registrationLimitError;
            }
            if (initialRegistrationError != null) {
                throw initialRegistrationError;
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

    synchronized PullRegistrationState registrationState() {
        boolean isReady = hasRegisteredRequiredSlotKinds;
        final List<PullQueueRegistrationState> snapshots = new ArrayList<PullQueueRegistrationState>();
        for (PollWorkerSlotKind slotKind : slotKinds) {
            final SlotRegistration registration = slotRegistrations.get(slotKind);
            final boolean hasRegisteredStream = registration.registeredStreams > 0;
            snapshots.add(new PullQueueRegistrationState(
                    slotKind.name(), hasRegisteredStream, registration.lastPermanentError));
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

    private void recordStreamResult(final PullStream stream, final Throwable failure) {
        final SlotRegistration registration = slotRegistrations.get(stream.slotKind);
        if (stream.isRegistered) {
            stream.isRegistered = false;
            registration.registeredStreams--;
        }
        if (!stream.shouldRetryRejectedTokenImmediately && isPermanentStreamError(failure)) {
            registration.lastPermanentError = failure;
            final PullRegistrationError rejection = new PullRegistrationError(stream.slotKind.name(), failure);
            if (registration.registeredStreams == 0) {
                if (!hasRegisteredRequiredSlotKinds && initialRegistrationError == null) {
                    initialRegistrationError = rejection;
                }
                if (options.isStopOnPermanentStreamRejection() && permanentStreamError == null && registrationLimitError == null) {
                    permanentStreamError = rejection;
                }
            }
        }
        notifyAll();
    }

    private boolean isPermanentStreamError(final Throwable failure) {
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
            }
            final long deadline = stopDeadlineNanos;
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
                    stream.cancelTask(Status.Code.CANCELLED);
                    if (!stream.isClosed && stream.transport != null) {
                        final RuntimeException cancellation = Status.CANCELLED.asRuntimeException();
                        try {
                            stream.transport.cancel("Worker stop timeout", cancellation);
                        } catch (RuntimeException failure) {
                            LOGGER.log(Level.WARNING, "cancel Worker task stream", failure);
                        } finally {
                            stream.close(cancellation);
                        }
                    }
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

    private void reconcile() {
        if (isStopping) {
            return;
        }
        if (streams.isEmpty()) {
            for (int slot = 0; slot < options.getMaxConcurrentTasks(); slot++) {
                final PollWorkerSlotKind slotKind = slot < options.getReservedRpcTasks()
                        ? PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY
                        : PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC;
                streams.add(new PullStream(slotKind));
            }
            for (PullStream stream : new ArrayList<PullStream>(streams)) {
                stream.open();
            }
        }
    }

    private long retryDelay(final long backoff) {
        return backoff + (long) (jitter.nextDouble() * backoff / 2.0);
    }

    private final class PullStream implements StreamObserver<PollWorkerTaskResponse> {
        private final PollWorkerSlotKind slotKind;
        private volatile AtomicReference<String> callToken;
        private volatile int openGeneration;
        private StreamObserver<PollWorkerTaskRequest> requests;
        private ClientCallStreamObserver<PollWorkerTaskRequest> transport;
        private PollWorkerTask pendingTask;
        private Context.CancellableContext taskContext;
        private ScheduledFuture<?> deadlineTimer;
        private ScheduledFuture<?> keepaliveTimer;
        private ScheduledFuture<?> closeTimer;
        private Thread handlerThread;
        private Throwable closeFailure;
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

        private PullStream(final PollWorkerSlotKind slotKind) {
            this.slotKind = slotKind;
        }

        private void open() {
            final int generation = ++openGeneration;
            final AtomicReference<String> openedToken = new AtomicReference<String>();
            callToken = openedToken;
            isClosed = false;
            isHalfClosed = false;
            isRegistered = false;
            pendingTask = null;
            isFinalFrameSent = false;
            closeFailure = null;
            try {
                final StreamObserver<PollWorkerTaskResponse> observer =
                        new ClientResponseObserver<PollWorkerTaskRequest, PollWorkerTaskResponse>() {
                            @Override
                            public void beforeStart(final ClientCallStreamObserver<PollWorkerTaskRequest> call) {
                                transport = call;
                            }

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
                GrpcCredential.withTokenCapture(openedToken).run(() ->
                        requests = taskService.pollWorkerTask(observer));
                send(PollWorkerTaskRequest.newBuilder()
                        .setRegister(PollWorkerTaskRegister.newBuilder()
                                .setSupportsReservedSlotDispatch(true)
                                .setSlotKind(slotKind)
                                .setAppWorkerId(appWorkerID)
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
                    if (response.hasReconnect()) {
                        isDraining = true;
                        halfClose();
                    } else if (response.hasRegistered()) {
                        final long serverLimit = Integer.toUnsignedLong(response.getRegistered()
                                .getMaxConcurrentTasksPerAppWorker());
                        if (serverLimit != 0 && serverLimit < options.getMaxConcurrentTasks()) {
                            registrationLimitError = new IllegalStateException("maxConcurrentTasks "
                                    + options.getMaxConcurrentTasks() + " exceeds Server setting "
                                    + "matching.maxConcurrentTasksCap " + serverLimit);
                            PullWorker.this.notifyAll();
                            return;
                        }
                        isRegistered = true;
                        recordRegisteredStream(this);
                        backoffMillis = 100L;
                        hasRetriedRejectedToken = false;
                        reconcile();
                    } else {
                        requests.onError(Status.INTERNAL
                                .withDescription("Worker task stream did not register")
                                .asRuntimeException());
                    }
                    return;
                }
                if (response.hasTask()) {
                    startTask(response.getTask());
                } else if (response.hasCancelTask()) {
                    if (response.getCancelTask().getTaskId().equals(taskID)) {
                        cancelTask(Status.Code.CANCELLED);
                    }
                } else if (response.hasPing()) {
                    send(PollWorkerTaskRequest.newBuilder()
                            .setPong(PollWorkerTaskPong.getDefaultInstance()).build());
                } else if (response.hasReconnect()) {
                    isDraining = true;
                    if (!isBusy) {
                        halfClose();
                    } else {
                        reconcile();
                    }
                }
            }
        }

        private void startTask(final PollWorkerTask task) {
            if (isStopping || isDraining || isHalfClosed || registrationLimitError != null) {
                return;
            }
            if (isBusy && isFinalFrameSent && pendingTask == null) {
                pendingTask = task;
                return;
            }
            if (isBusy) {
                final RuntimeException failure = Status.INTERNAL
                        .withDescription("Server assigned two tasks to one reserved slot").asRuntimeException();
                requests.onError(failure);
                close(failure);
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
            keepaliveTimer = scheduler.scheduleAtFixedRate(() -> {
                synchronized (PullWorker.this) {
                    if (isBusy && !isFinalFrameSent && !isHalfClosed && !isClosed) {
                        send(PollWorkerTaskRequest.newBuilder()
                                .setPong(PollWorkerTaskPong.getDefaultInstance()).build());
                    }
                }
            }, BUSY_PULL_STREAM_KEEPALIVE_SECONDS, BUSY_PULL_STREAM_KEEPALIVE_SECONDS, TimeUnit.SECONDS);
            if (task.hasDeadline()) {
                final Instant deadline = Instant.ofEpochSecond(
                        task.getDeadline().getSeconds(), task.getDeadline().getNanos());
                final long delay = Duration.between(Instant.now(), deadline).toMillis();
                if (delay <= 0L) {
                    cancelTask(Status.Code.DEADLINE_EXCEEDED);
                } else {
                    final String deadlineTaskID = taskID;
                    deadlineTimer = scheduler.schedule(() -> {
                        synchronized (PullWorker.this) {
                            if (deadlineTaskID.equals(taskID)) {
                                cancelTask(Status.Code.DEADLINE_EXCEEDED);
                            }
                        }
                    },
                            delay, TimeUnit.MILLISECONDS);
                }
            }
            try {
                handlers.execute(() -> runTask(task));
            } catch (RejectedExecutionException rejected) {
                requests.onError(rejected);
                close(rejected);
                finishTask();
                return;
            }
            reconcile();
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
                if (!isHalfClosed && !isClosed && requests != null
                        && (!request.hasPong() || !isFinalFrameSent)) {
                    try {
                        requests.onNext(request);
                    } catch (RuntimeException failure) {
                        try {
                            transport.cancel("Worker task frame send failed", failure);
                        } finally {
                            close(failure);
                        }
                        throw failure;
                    }
                }
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
            if (isClosed) {
                retry(closeFailure);
            } else if (isDraining || isStopping) {
                halfClose();
            } else if (pendingTask != null) {
                final PollWorkerTask nextTask = pendingTask;
                pendingTask = null;
                startTask(nextTask);
            }
            PullWorker.this.notifyAll();
        }

        private void halfClose() {
            stopKeepalive();
            if (isClosed) {
                if (!isBusy && isStopping) {
                    streams.remove(this);
                    PullWorker.this.notifyAll();
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
            try {
                requests.onCompleted();
            } catch (RuntimeException failure) {
                close(failure);
                return;
            }
            scheduleCloseFallback();
        }

        private void scheduleCloseFallback() {
            if (closeTimer != null) {
                closeTimer.cancel(false);
            }
            final long waitNanos = isStopping
                    ? Math.max(1L, stopDeadlineNanos - System.nanoTime())
                    : options.getStopDrainTimeout().toNanos();
            closeTimer = scheduler.schedule(() -> {
                synchronized (PullWorker.this) {
                    if (!isClosed) {
                        final RuntimeException timeout = Status.DEADLINE_EXCEEDED
                                .withDescription("Worker stream close timed out")
                                .asRuntimeException();
                        try {
                            transport.cancel("Worker stream close timed out", timeout);
                        } catch (RuntimeException failure) {
                            LOGGER.log(Level.WARNING, "cancel timed-out Worker task stream", failure);
                        } finally {
                            close(timeout);
                        }
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

        private void close(final Throwable failure, final String rejectedToken,
                final int generation) {
            synchronized (PullWorker.this) {
                if (isClosed || generation != openGeneration) {
                    return;
                }
                shouldRetryRejectedTokenImmediately = failure != null
                        && Status.fromThrowable(failure).getCode() == Status.Code.UNAUTHENTICATED
                        && credential instanceof RejectedTokenCredential && rejectedToken != null
                        && !hasRetriedRejectedToken;
                if (failure != null
                        && Status.fromThrowable(failure).getCode() == Status.Code.UNAUTHENTICATED) {
                    GrpcCredential.rejectToken(credential, rejectedToken);
                }
                isClosed = true;
                recordStreamResult(this, failure);
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
                streams.remove(this);
                PullWorker.this.notifyAll();
                return;
            }
            final Status.Code code = failure == null ? Status.Code.OK
                    : Status.fromThrowable(failure).getCode();
            final boolean isPermanent = code == Status.Code.UNAUTHENTICATED
                    || code == Status.Code.PERMISSION_DENIED
                    || code == Status.Code.INVALID_ARGUMENT
                    || code == Status.Code.UNIMPLEMENTED
                    || code == Status.Code.RESOURCE_EXHAUSTED;
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
            scheduler.schedule(() -> {
                synchronized (PullWorker.this) {
                    if (!isStopping) {
                        open();
                    }
                }
            }, delay, TimeUnit.MILLISECONDS);
            PullWorker.this.notifyAll();
        }
    }
}
