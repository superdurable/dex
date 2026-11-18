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

import com.google.common.io.ByteStreams;
import com.google.protobuf.InvalidProtocolBufferException;
import io.grpc.CallOptions;
import io.grpc.ClientCall;
import io.grpc.HandlerRegistry;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Server;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerMethodDefinition;
import io.grpc.Status;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.superdurable.gen.PollWorkerSlotKind;
import io.superdurable.gen.PollWorkerTaskRequest;
import io.superdurable.gen.PollWorkerTaskResponse;
import io.superdurable.gen.WorkerTaskServiceGrpc;

import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.UncheckedIOException;
import java.net.InetSocketAddress;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.BlockingQueue;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.LinkedBlockingQueue;
import java.util.concurrent.TimeUnit;

// Forwards a Worker's gRPC traffic to the current server and observes and gates its slot streams.
final class PullSlotProxy implements AutoCloseable {
    static final String RESET_MESSAGE = "pull slot proxy reset the stream";
    private static final String POLL_METHOD = WorkerTaskServiceGrpc.getPollWorkerTaskMethod().getFullMethodName();
    private static final MethodDescriptor.Marshaller<byte[]> BYTES = new MethodDescriptor.Marshaller<byte[]>() {
        @Override
        public InputStream stream(final byte[] value) {
            return new ByteArrayInputStream(value);
        }

        @Override
        public byte[] parse(final InputStream stream) {
            try {
                return ByteStreams.toByteArray(stream);
            } catch (IOException unreadable) {
                throw new UncheckedIOException(unreadable);
            }
        }
    };

    private final Server server;
    private final ExecutorService gateThreads = Executors.newCachedThreadPool(runnable -> {
        final Thread thread = new Thread(runnable, "pull-slot-proxy-gate");
        thread.setDaemon(true);
        return thread;
    });
    // Everything below is guarded by this proxy's monitor.
    private ManagedChannel upstream;
    private final Set<ProxyStream> streams = new HashSet<ProxyStream>();
    private final List<ProxyEvent> registers = new ArrayList<ProxyEvent>();
    private final List<ProxyEvent> acknowledgements = new ArrayList<ProxyEvent>();
    private final List<ProxyEvent> rejections = new ArrayList<ProxyEvent>();
    private final List<ProxyEvent> workerResets = new ArrayList<ProxyEvent>();
    private StepRegisterGate stepRegisterGate;
    private CountDownLatch acknowledgementGate;
    private boolean shouldFailStepRegisters;
    private int passStepRegisters;
    private CountDownLatch halfCloseGate;
    private BlockingQueue<Boolean> heldHalfCloses;
    private long firstAcknowledgementAtNanos;

    // One Register, acknowledgement, rejection, or reset of an app Worker's slot stream.
    static final class ProxyEvent {
        final String appWorkerID;
        final PollWorkerSlotKind slotKind;
        final long atNanos;

        private ProxyEvent(final String appWorkerID, final PollWorkerSlotKind slotKind) {
            this.appWorkerID = appWorkerID;
            this.slotKind = slotKind;
            this.atNanos = System.nanoTime();
        }
    }

    // Holds STEP_AND_RPC Registers; isReleasedByRpcOnly releases it at the first RPC_ONLY acknowledgement.
    private static final class StepRegisterGate {
        private final CountDownLatch release = new CountDownLatch(1);
        private final boolean isReleasedByRpcOnly;
        private final List<ProxyStream> held = new ArrayList<ProxyStream>();
        // Opens when the first STEP_AND_RPC Register waits; an RPC_ONLY Register waits for it.
        private final CountDownLatch stepHeld = new CountDownLatch(1);

        private StepRegisterGate(final boolean isReleasedByRpcOnly) {
            this.isReleasedByRpcOnly = isReleasedByRpcOnly;
        }
    }

    // A half-close gate and the signal for each half-close it holds.
    static final class HalfCloseHold {
        final BlockingQueue<Boolean> held;
        final Runnable release;

        private HalfCloseHold(final BlockingQueue<Boolean> held, final Runnable release) {
            this.held = held;
            this.release = release;
        }
    }

    PullSlotProxy(final String upstreamAddress) throws IOException {
        setUpstream(upstreamAddress);
        final ServerCallHandler<byte[], byte[]> handler = this::startCall;
        server = NettyServerBuilder.forAddress(new InetSocketAddress("127.0.0.1", 0))
                .fallbackHandlerRegistry(new HandlerRegistry() {
                    @Override
                    public ServerMethodDefinition<?, ?> lookupMethod(final String methodName, final String authority) {
                        return ServerMethodDefinition.create(bytesMethod(methodName), handler);
                    }
                })
                .build().start();
    }

    String address() {
        return "127.0.0.1:" + server.getPort();
    }

    // Points new calls at address; calls already open stay on the old server.
    void setUpstream(final String address) {
        final ManagedChannel channel = ManagedChannelBuilder.forTarget(address).usePlaintext().build();
        final ManagedChannel previous;
        synchronized (this) {
            previous = upstream;
            upstream = channel;
        }
        if (previous != null) {
            previous.shutdownNow();
        }
    }

    private synchronized ManagedChannel currentUpstream() {
        return upstream;
    }

    private static MethodDescriptor<byte[], byte[]> bytesMethod(final String methodName) {
        return MethodDescriptor.<byte[], byte[]>newBuilder()
                .setType(MethodDescriptor.MethodType.BIDI_STREAMING)
                .setFullMethodName(methodName)
                .setRequestMarshaller(BYTES)
                .setResponseMarshaller(BYTES)
                .build();
    }

    private ServerCall.Listener<byte[]> startCall(final ServerCall<byte[], byte[]> call, final Metadata headers) {
        final String methodName = call.getMethodDescriptor().getFullMethodName();
        if (POLL_METHOD.equals(methodName)) {
            return new ProxyStream(call, copyHeaders(headers)).listener();
        }
        return forwardPlainCall(call, methodName, copyHeaders(headers));
    }

    private ServerCall.Listener<byte[]> forwardPlainCall(final ServerCall<byte[], byte[]> call,
            final String methodName, final Metadata headers) {
        final Object callLock = new Object();
        final ClientCall<byte[], byte[]> clientCall =
                currentUpstream().newCall(bytesMethod(methodName), CallOptions.DEFAULT);
        clientCall.start(new ClientCall.Listener<byte[]>() {
            @Override
            public void onHeaders(final Metadata responseHeaders) {
                synchronized (callLock) {
                    call.sendHeaders(responseHeaders);
                }
            }

            @Override
            public void onMessage(final byte[] message) {
                synchronized (callLock) {
                    call.sendMessage(message);
                }
            }

            @Override
            public void onClose(final Status status, final Metadata trailers) {
                synchronized (callLock) {
                    call.close(status, trailers);
                }
            }
        }, headers);
        clientCall.request(Integer.MAX_VALUE);
        call.request(Integer.MAX_VALUE);
        return new ServerCall.Listener<byte[]>() {
            @Override
            public void onMessage(final byte[] message) {
                clientCall.sendMessage(message);
            }

            @Override
            public void onHalfClose() {
                clientCall.halfClose();
            }

            @Override
            public void onCancel() {
                clientCall.cancel("downstream cancelled", null);
            }
        };
    }

    // Copies application headers; gRPC transport headers are set again by the upstream call.
    private static Metadata copyHeaders(final Metadata headers) {
        final Metadata copied = new Metadata();
        for (String key : headers.keys()) {
            if (key.startsWith(":") || key.startsWith("grpc-") || "content-type".equals(key)
                    || "user-agent".equals(key) || "te".equals(key)) {
                continue;
            }
            if (key.endsWith(Metadata.BINARY_HEADER_SUFFIX)) {
                final Metadata.Key<byte[]> binaryKey = Metadata.Key.of(key, Metadata.BINARY_BYTE_MARSHALLER);
                for (byte[] value : headers.getAll(binaryKey)) {
                    copied.put(binaryKey, value);
                }
            } else {
                final Metadata.Key<String> asciiKey = Metadata.Key.of(key, Metadata.ASCII_STRING_MARSHALLER);
                for (String value : headers.getAll(asciiKey)) {
                    copied.put(asciiKey, value);
                }
            }
        }
        return copied;
    }

    // Forces the reconnect order of design Test 17: a STEP_AND_RPC Register waits, the RPC_ONLY stream
    // registers, and the waiting Register reaches the server before the Worker sees that acknowledgement.
    synchronized void holdStepRegistersUntilRpcOnlyAcknowledged() {
        stepRegisterGate = new StepRegisterGate(true);
    }

    // Holds STEP_AND_RPC Registers until the returned release runs.
    Runnable holdStepRegisters() {
        final StepRegisterGate gate = new StepRegisterGate(false);
        synchronized (this) {
            stepRegisterGate = gate;
        }
        return () -> {
            synchronized (this) {
                if (stepRegisterGate == gate) {
                    stepRegisterGate = null;
                }
            }
            gate.release.countDown();
        };
    }

    // Counts the STEP_AND_RPC Registers that the manual gate holds now.
    synchronized int heldStepRegisters() {
        if (stepRegisterGate == null) {
            return 0;
        }
        int count = 0;
        for (ProxyStream held : stepRegisterGate.held) {
            if (!held.hasEnded) {
                count++;
            }
        }
        return count;
    }

    // Answers every later STEP_AND_RPC Register with UNAVAILABLE until stopFailingStepRegisters.
    synchronized void failStepRegistersFromNow() {
        shouldFailStepRegisters = true;
        passStepRegisters = 0;
    }

    // Lets one STEP_AND_RPC Register through while Registers fail.
    synchronized void passNextStepRegister() {
        passStepRegisters++;
    }

    synchronized void stopFailingStepRegisters() {
        shouldFailStepRegisters = false;
    }

    // Keeps every Worker half-close from reaching the server until release runs.
    HalfCloseHold holdHalfCloses() {
        final CountDownLatch gate = new CountDownLatch(1);
        final BlockingQueue<Boolean> held = new LinkedBlockingQueue<Boolean>();
        synchronized (this) {
            halfCloseGate = gate;
            heldHalfCloses = held;
        }
        return new HalfCloseHold(held, () -> {
            synchronized (this) {
                halfCloseGate = null;
            }
            gate.countDown();
        });
    }

    // Delays every acknowledgement until the returned release runs.
    Runnable holdAcknowledgements() {
        final CountDownLatch gate = new CountDownLatch(1);
        synchronized (this) {
            acknowledgementGate = gate;
        }
        return () -> {
            synchronized (this) {
                acknowledgementGate = null;
            }
            gate.countDown();
        };
    }

    // Resets one acknowledged stream of the kind, like a network failure.
    boolean resetRegisteredStream(final String appWorkerID, final PollWorkerSlotKind slotKind) {
        ProxyStream target = null;
        synchronized (this) {
            for (ProxyStream stream : streams) {
                if (appWorkerID.equals(stream.appWorkerID) && stream.slotKind == slotKind && stream.isRegistered
                        && !stream.hasEnded) {
                    target = stream;
                    break;
                }
            }
        }
        if (target == null) {
            return false;
        }
        target.reset();
        return true;
    }

    // Counts the acknowledged, live streams of a kind, as the server holds them.
    synchronized int registeredStreams(final String appWorkerID, final PollWorkerSlotKind slotKind) {
        int count = 0;
        for (ProxyStream stream : streams) {
            if (appWorkerID.equals(stream.appWorkerID) && stream.slotKind == slotKind && stream.isRegistered
                    && !stream.hasEnded) {
                count++;
            }
        }
        return count;
    }

    synchronized List<ProxyEvent> registersSince(final String appWorkerID, final long sinceNanos) {
        return filter(registers, appWorkerID, sinceNanos);
    }

    synchronized List<ProxyEvent> acknowledgementsSince(final String appWorkerID, final long sinceNanos) {
        return filter(acknowledgements, appWorkerID, sinceNanos);
    }

    synchronized List<ProxyEvent> rejectionsSince(final String appWorkerID, final long sinceNanos) {
        return filter(rejections, appWorkerID, sinceNanos);
    }

    synchronized List<ProxyEvent> workerResetsSince(final String appWorkerID, final long sinceNanos) {
        return filter(workerResets, appWorkerID, sinceNanos);
    }

    synchronized long firstAcknowledgementAtNanos() {
        return firstAcknowledgementAtNanos;
    }

    // Summarizes what the proxy saw of appWorkerID, for failure messages.
    synchronized String describe(final String appWorkerID) {
        final StringBuilder description = new StringBuilder();
        describeEvents(description, "registers", registers, appWorkerID);
        describeEvents(description, "acknowledgements", acknowledgements, appWorkerID);
        describeEvents(description, "rejections", rejections, appWorkerID);
        describeEvents(description, "workerResets", workerResets, appWorkerID);
        for (ProxyStream stream : streams) {
            if (appWorkerID.equals(stream.appWorkerID)) {
                description.append("live ").append(stream.slotKind).append(" registered=").append(stream.isRegistered)
                        .append(" stage=").append(stream.stage).append('\n');
            }
        }
        return description.toString();
    }

    private static void describeEvents(final StringBuilder description, final String name,
            final List<ProxyEvent> events, final String appWorkerID) {
        description.append(name).append(':');
        for (ProxyEvent event : filter(events, appWorkerID, Long.MIN_VALUE)) {
            description.append(' ').append(event.slotKind.name().replace("POLL_WORKER_SLOT_KIND_", ""))
                    .append('@').append(TimeUnit.NANOSECONDS.toMillis(event.atNanos));
        }
        description.append('\n');
    }

    private static List<ProxyEvent> filter(final List<ProxyEvent> events, final String appWorkerID,
            final long sinceNanos) {
        final List<ProxyEvent> matching = new ArrayList<ProxyEvent>();
        for (ProxyEvent event : events) {
            // Long.MIN_VALUE selects every event; nanoTime differences would overflow against it.
            if (appWorkerID.equals(event.appWorkerID)
                    && (sinceNanos == Long.MIN_VALUE || event.atNanos - sinceNanos >= 0)) {
                matching.add(event);
            }
        }
        return matching;
    }

    @Override
    public void close() throws InterruptedException {
        server.shutdownNow();
        server.awaitTermination(10, TimeUnit.SECONDS);
        final ManagedChannel channel;
        synchronized (this) {
            channel = upstream;
            final StepRegisterGate gate = stepRegisterGate;
            if (gate != null) {
                gate.release.countDown();
            }
            if (acknowledgementGate != null) {
                acknowledgementGate.countDown();
            }
            if (halfCloseGate != null) {
                halfCloseGate.countDown();
            }
        }
        channel.shutdownNow();
        gateThreads.shutdownNow();
    }

    // One proxied PollWorkerTask call; the upstream call opens only after the Register passes its gate.
    private final class ProxyStream {
        private final ServerCall<byte[], byte[]> call;
        private final Metadata headers;
        // Serializes every use of call and upstreamCall, which are not thread-safe.
        private final Object callLock = new Object();
        private final CountDownLatch downstreamEnded = new CountDownLatch(1);
        // Opens when the server sends the first frame or ends the stream.
        private final CountDownLatch upstreamAnswered = new CountDownLatch(1);
        private final List<byte[]> pendingFrames = new ArrayList<byte[]>();
        private ClientCall<byte[], byte[]> upstreamCall;
        private boolean isDownstreamHalfClosed;
        private boolean isDownstreamClosed;
        private volatile boolean isReset;
        private volatile String stage = "new";
        // The fields below are guarded by the proxy's monitor.
        private String appWorkerID;
        private PollWorkerSlotKind slotKind;
        private boolean isRegistered;
        private boolean hasEnded;
        private ProxyEvent register;

        private ProxyStream(final ServerCall<byte[], byte[]> call, final Metadata headers) {
            this.call = call;
            this.headers = headers;
        }

        private ServerCall.Listener<byte[]> listener() {
            call.request(Integer.MAX_VALUE);
            return new ServerCall.Listener<byte[]>() {
                @Override
                public void onMessage(final byte[] message) {
                    if (register == null) {
                        receiveRegister(message);
                        return;
                    }
                    synchronized (callLock) {
                        if (upstreamCall != null) {
                            upstreamCall.sendMessage(message);
                        } else {
                            pendingFrames.add(message);
                        }
                    }
                }

                @Override
                public void onHalfClose() {
                    gateThreads.execute(ProxyStream.this::forwardHalfClose);
                }

                @Override
                public void onCancel() {
                    downstreamEnded.countDown();
                    final boolean isWorkerReset;
                    synchronized (callLock) {
                        isWorkerReset = !isReset && !isDownstreamClosed;
                        isDownstreamClosed = true;
                        if (upstreamCall != null) {
                            upstreamCall.cancel("the Worker reset the stream", null);
                        }
                    }
                    synchronized (PullSlotProxy.this) {
                        if (isWorkerReset && register != null) {
                            workerResets.add(new ProxyEvent(appWorkerID, slotKind));
                        }
                    }
                    markEnded();
                }

                @Override
                public void onComplete() {
                    downstreamEnded.countDown();
                    markEnded();
                }
            };
        }

        private void receiveRegister(final byte[] message) {
            final PollWorkerTaskRequest request;
            try {
                request = PollWorkerTaskRequest.parseFrom(message);
            } catch (InvalidProtocolBufferException malformed) {
                closeDownstream(Status.INVALID_ARGUMENT.withDescription("proxy: decode first frame"));
                return;
            }
            final boolean isStep;
            final boolean shouldFail;
            final StepRegisterGate gate;
            synchronized (PullSlotProxy.this) {
                appWorkerID = request.getRegister().getAppWorkerId();
                slotKind = request.getRegister().getSlotKind();
                register = new ProxyEvent(appWorkerID, slotKind);
                registers.add(register);
                streams.add(this);
                isStep = slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_STEP_AND_RPC;
                gate = stepRegisterGate;
                if (isStep && gate != null) {
                    gate.held.add(this);
                    gate.stepHeld.countDown();
                }
                shouldFail = isStep && shouldFailStepRegisters && passStepRegisters == 0;
                if (isStep && shouldFailStepRegisters && passStepRegisters > 0) {
                    passStepRegisters--;
                }
            }
            if (shouldFail) {
                upstreamAnswered.countDown();
                closeDownstream(Status.UNAVAILABLE.withDescription("pull slot proxy failed the Register"));
                markEnded();
                return;
            }
            gateThreads.execute(() -> openUpstream(message, isStep, gate));
        }

        private void openUpstream(final byte[] firstFrame, final boolean isStep, final StepRegisterGate gate) {
            stage = "arrived";
            try {
                if (isStep && gate != null) {
                    stage = "gated";
                    if (!awaitEither(gate.release)) {
                        return;
                    }
                }
                // The RPC_ONLY stream registers only once a STEP_AND_RPC Register waits, so that Register meets a full cap.
                if (!isStep && gate != null && gate.isReleasedByRpcOnly) {
                    stage = "waiting for a held Step-and-RPC Register";
                    if (!awaitEither(gate.stepHeld)) {
                        return;
                    }
                }
            } catch (InterruptedException interrupted) {
                Thread.currentThread().interrupt();
                return;
            }
            stage = "opening upstream";
            final ClientCall<byte[], byte[]> opened =
                    currentUpstream().newCall(bytesMethod(POLL_METHOD), CallOptions.DEFAULT);
            opened.start(new UpstreamListener(), headers);
            opened.request(Integer.MAX_VALUE);
            synchronized (callLock) {
                if (isDownstreamClosed) {
                    opened.cancel("the downstream stream already ended", null);
                    upstreamAnswered.countDown();
                    return;
                }
                upstreamCall = opened;
                opened.sendMessage(firstFrame);
                for (byte[] frame : pendingFrames) {
                    opened.sendMessage(frame);
                }
                pendingFrames.clear();
                if (isDownstreamHalfClosed) {
                    opened.halfClose();
                }
            }
            stage = "register sent upstream";
        }

        // Returns false when the downstream stream ended first.
        private boolean awaitEither(final CountDownLatch latch) throws InterruptedException {
            while (!latch.await(10, TimeUnit.MILLISECONDS)) {
                if (downstreamEnded.getCount() == 0) {
                    upstreamAnswered.countDown();
                    return false;
                }
            }
            return true;
        }

        private void forwardHalfClose() {
            final CountDownLatch gate;
            final BlockingQueue<Boolean> held;
            synchronized (PullSlotProxy.this) {
                gate = halfCloseGate;
                held = heldHalfCloses;
            }
            if (gate != null) {
                held.add(Boolean.TRUE);
                try {
                    awaitEither(gate);
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    return;
                }
            }
            synchronized (callLock) {
                if (upstreamCall != null) {
                    upstreamCall.halfClose();
                } else {
                    isDownstreamHalfClosed = true;
                }
            }
        }

        private void reset() {
            isReset = true;
            synchronized (callLock) {
                if (upstreamCall != null) {
                    upstreamCall.cancel(RESET_MESSAGE, null);
                }
            }
            closeDownstream(Status.UNAVAILABLE.withDescription(RESET_MESSAGE));
            markEnded();
        }

        private void closeDownstream(final Status status) {
            synchronized (callLock) {
                if (!isDownstreamClosed) {
                    isDownstreamClosed = true;
                    call.close(status, new Metadata());
                }
            }
        }

        private void markEnded() {
            synchronized (PullSlotProxy.this) {
                hasEnded = true;
                streams.remove(this);
            }
        }

        private void recordAcknowledgement() {
            final CountDownLatch ackGate;
            synchronized (PullSlotProxy.this) {
                ackGate = acknowledgementGate;
            }
            if (ackGate != null) {
                try {
                    ackGate.await();
                } catch (InterruptedException interrupted) {
                    Thread.currentThread().interrupt();
                    return;
                }
            }
            final StepRegisterGate gate;
            final boolean isReleasingStepRegisters;
            synchronized (PullSlotProxy.this) {
                isRegistered = true;
                acknowledgements.add(new ProxyEvent(appWorkerID, slotKind));
                if (firstAcknowledgementAtNanos == 0L) {
                    firstAcknowledgementAtNanos = System.nanoTime();
                }
                gate = stepRegisterGate;
                isReleasingStepRegisters = gate != null && gate.isReleasedByRpcOnly
                        && slotKind == PollWorkerSlotKind.POLL_WORKER_SLOT_KIND_RPC_ONLY;
                if (isReleasingStepRegisters) {
                    stepRegisterGate = null;
                }
            }
            if (isReleasingStepRegisters) {
                gate.release.countDown();
                // The Worker sees this acknowledgement only after the server answered every held Register.
                for (ProxyStream held : gate.held) {
                    try {
                        if (!held.upstreamAnswered.await(10, TimeUnit.SECONDS)) {
                            throw new AssertionError("a held Register got no answer from the server");
                        }
                    } catch (InterruptedException interrupted) {
                        Thread.currentThread().interrupt();
                        return;
                    }
                }
            }
        }

        private final class UpstreamListener extends ClientCall.Listener<byte[]> {
            @Override
            public void onHeaders(final Metadata responseHeaders) {
                synchronized (callLock) {
                    if (!isDownstreamClosed) {
                        call.sendHeaders(responseHeaders);
                    }
                }
            }

            @Override
            public void onMessage(final byte[] message) {
                upstreamAnswered.countDown();
                try {
                    if (PollWorkerTaskResponse.parseFrom(message).hasRegistered()) {
                        recordAcknowledgement();
                    }
                } catch (InvalidProtocolBufferException malformed) {
                    throw new AssertionError("proxy: the server sent a malformed frame", malformed);
                }
                synchronized (callLock) {
                    if (!isDownstreamClosed) {
                        call.sendMessage(message);
                    }
                }
            }

            @Override
            public void onClose(final Status status, final Metadata trailers) {
                upstreamAnswered.countDown();
                synchronized (PullSlotProxy.this) {
                    if (status.getCode() == Status.Code.RESOURCE_EXHAUSTED && register != null) {
                        rejections.add(register);
                    }
                }
                synchronized (callLock) {
                    if (!isDownstreamClosed) {
                        isDownstreamClosed = true;
                        if (isReset) {
                            call.close(Status.UNAVAILABLE.withDescription(RESET_MESSAGE), new Metadata());
                        } else {
                            call.close(status, trailers);
                        }
                    }
                }
                markEnded();
            }
        }
    }
}
