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

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.StatusException;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.StreamObserver;
import io.superdurable.dex.exceptions.InvalidStepResultException;
import io.superdurable.gen.CloseDecisionType;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeExecuteMethodResponse;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.LoadBlobsRequest;
import io.superdurable.gen.LoadBlobsResponse;
import io.superdurable.gen.Value;
import io.superdurable.gen.WorkerServiceGrpc;
import io.superdurable.gen.WorkerErrorResponse;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.ServerSocket;
import java.nio.charset.StandardCharsets;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class WorkerServiceIntegrationTest {
    @Test
    void derivesAdvertisedTargetFromDefaultBindAddress() {
        final Worker worker = new Worker(
                new Registry(Collections.<Flow<?>>emptyList()),
                new TestBlobCache());
        try {
            assertEquals("localhost:8803", worker.getWorkerTarget().getAddress());
            assertFalse(worker.getWorkerTarget().isHeadless());
        } finally {
            worker.close();
        }
    }

    @Test
    void routesWorkerProtobufEntirelyInsideTheJvm() throws Exception {
        final BridgeFlow flow = new BridgeFlow();
        final RunningWorker running = startWorker(flow, new TestBlobCache(), null);
        try {
            final InvokeWaitForMethodResponse wait = running.client.invokeWaitForMethod(
                    waitRequest(concrete("hello")));
            assertFalse(wait.hasWaitingCondition());

            final InvokeExecuteMethodResponse execute = running.client.invokeExecuteMethod(
                    executeRequest(concrete("hello")));
            assertEquals(
                    CloseDecisionType.CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE,
                    execute.getStepDecision().getCloseDecision().getCloseDecisionType());
            assertEquals(
                    "hello",
                    execute.getStepDecision().getCloseDecision().getCloseInput().getStringValue());
            assertTrue(flow.start.handlerThread.get().startsWith("dex-java-handler-"));
        } finally {
            running.close();
        }
    }

    @Test
    void preservesJavaFailureTypeAndMessage() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            final StatusRuntimeException failure = assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(executeRequest(concrete("fail"))));
            assertEquals(Status.Code.INTERNAL, failure.getStatus().getCode());
            final com.google.rpc.Status status = StatusProto.fromThrowable(failure);
            final WorkerErrorResponse details = status.getDetails(0)
                    .unpack(WorkerErrorResponse.class);
            assertEquals(BridgeFailureException.class.getName(), details.getErrorType());
            assertEquals("bridge failed", details.getDetail());
            assertTrue(details.getStackTrace().contains(BridgeFailureException.class.getName()));
            assertTrue(details.getStackTrace().contains("bridge failed"));
        } finally {
            running.close();
        }
    }

    @Test
    void mapsEveryUnconfiguredApplicationThrowableToInternal() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            assertWorkerFailure(running, "checked", CheckedBridgeException.class);
            assertWorkerFailure(running, "error", AssertionError.class);
            assertWorkerFailure(running, "status", StatusRuntimeException.class);
            assertWorkerFailure(running, "checked-status", StatusException.class);
        } finally {
            running.close();
        }
    }

    @Test
    void preservesTheCauseOfCheckedRpcExceptions() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            final StatusRuntimeException failure = assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeWorkerRPC(InvokeWorkerRPCRequest.newBuilder()
                            .setContext(context())
                            .setFlowType("BridgeFlow")
                            .setRpcName("checkedRpc")
                            .setInput(concrete("unused"))
                            .build()));
            assertEquals(Status.Code.INTERNAL, failure.getStatus().getCode());
            final WorkerErrorResponse details = StatusProto.fromThrowable(failure)
                    .getDetails(0)
                    .unpack(WorkerErrorResponse.class);
            assertEquals(CheckedBridgeException.class.getName(), details.getErrorType());
            assertEquals("checked RPC failure", details.getDetail());
            assertTrue(details.getStackTrace().contains("checkedRpc"));
        } finally {
            running.close();
        }
    }

    @Test
    void usesTheClosestConfiguredExceptionSuperclass() throws Exception {
        final GrpcErrorStatusMapping mapping = GrpcErrorStatusMapping.newBuilder()
                .map(Throwable.class, Status.Code.ABORTED)
                .map(RuntimeException.class, Status.Code.FAILED_PRECONDITION)
                .build();
        final RunningWorker running = startWorker(
                new BridgeFlow(),
                new TestBlobCache(),
                null,
                mapping);
        try {
            final StatusRuntimeException failure = assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(executeRequest(concrete("fail"))));
            assertEquals(Status.Code.FAILED_PRECONDITION, failure.getStatus().getCode());
        } finally {
            running.close();
        }
    }

    @Test
    void truncatesPersistedStackTraceAtUtf8Boundary() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            final StatusRuntimeException failure = assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(executeRequest(concrete("large"))));
            final WorkerErrorResponse details = StatusProto.fromThrowable(failure)
                    .getDetails(0)
                    .unpack(WorkerErrorResponse.class);
            assertTrue(details.getStackTrace().endsWith(
                    "... stack trace truncated by Dex Java SDK ..."));
            assertTrue(details.getStackTrace().getBytes(StandardCharsets.UTF_8).length <= 16 * 1024);
            assertFalse(details.getStackTrace().contains("\ufffd"));
        } finally {
            running.close();
        }
    }

    @Test
    void reportsInvalidStepResultTypeAndContext() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            final StatusRuntimeException failure = assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(executeRequest(concrete("invalid"))));
            final com.google.rpc.Status status = StatusProto.fromThrowable(failure);
            final WorkerErrorResponse details = status.getDetails(0)
                    .unpack(WorkerErrorResponse.class);
            assertEquals(Status.Code.INTERNAL, failure.getStatus().getCode());
            assertEquals(InvalidStepResultException.class.getName(), details.getErrorType());
            assertTrue(details.getDetail().contains("Flow BridgeFlow Step BridgeStep"));
        } finally {
            running.close();
        }
    }

    @Test
    void hydratesBlobValuesThroughTheJavaCacheInterface() throws Exception {
        final AtomicInteger loads = new AtomicInteger();
        final int flowPort = availablePort();
        final Server flowServer = ServerBuilder.forPort(flowPort)
                .addService(new FlowServiceGrpc.FlowServiceImplBase() {
                    @Override
                    public void loadBlobs(
                            final LoadBlobsRequest request,
                            final StreamObserver<LoadBlobsResponse> observer) {
                        loads.incrementAndGet();
                        final LoadBlobsResponse.Builder response = LoadBlobsResponse.newBuilder();
                        for (Value value : request.getValuesList()) {
                            final String blobId = value.getInternalBlobIdForStringValue();
                            response.putValues(blobId, concrete("hydrated"));
                        }
                        observer.onNext(response.build());
                        observer.onCompleted();
                    }
                })
                .build()
                .start();
        final TestBlobCache cache = new TestBlobCache();
        final RunningWorker running = startWorker(
                new BridgeFlow(),
                cache,
                "127.0.0.1:" + flowPort);
        final Value blob = Value.newBuilder()
                .setInternalBlobIdForStringValue("blob-1")
                .build();
        try {
            assertEquals(
                    "hydrated",
                    running.client.invokeExecuteMethod(executeRequest(blob))
                            .getStepDecision()
                            .getCloseDecision()
                            .getCloseInput()
                            .getStringValue());
            assertEquals(
                    "hydrated",
                    running.client.invokeExecuteMethod(executeRequest(blob))
                            .getStepDecision()
                            .getCloseDecision()
                            .getCloseInput()
                            .getStringValue());
            assertEquals(1, loads.get());
            assertTrue(cache.get("blob-1").isPresent());
        } finally {
            running.close();
            flowServer.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    private static RunningWorker startWorker(
            final BridgeFlow flow,
            final TestBlobCache cache,
            final String serverAddress) throws Exception {
        return startWorker(flow, cache, serverAddress, null);
    }

    private static RunningWorker startWorker(
            final BridgeFlow flow,
            final TestBlobCache cache,
            final String serverAddress,
            final GrpcErrorStatusMapping mapping) throws Exception {
        final int port = availablePort();
        final WorkerOptions.Builder options = WorkerOptions.newBuilder()
                .bindAddress("127.0.0.1:" + port);
        if (serverAddress != null) {
            options.serverAddress(serverAddress);
        }
        if (mapping != null) {
            options.grpcErrorStatusMapping(mapping);
        }
        final Worker worker = new Worker(
                new Registry(Collections.<Flow<?>>singletonList(flow)),
                cache,
                options.build());
        final AtomicReference<Throwable> workerFailure = new AtomicReference<Throwable>();
        final Thread workerThread = new Thread(() -> {
            try {
                worker.start();
            } catch (Throwable failure) {
                workerFailure.set(failure);
            }
        }, "test-dex-worker");
        workerThread.start();
        final ManagedChannel channel = ManagedChannelBuilder
                .forAddress("127.0.0.1", port)
                .usePlaintext()
                .maxInboundMetadataSize(64 * 1024)
                .build();
        final WorkerServiceGrpc.WorkerServiceBlockingStub client =
                WorkerServiceGrpc.newBlockingStub(channel)
                        .withWaitForReady()
                        .withDeadlineAfter(10, TimeUnit.SECONDS);
        return new RunningWorker(worker, workerThread, workerFailure, channel, client);
    }

    private static InvokeWaitForMethodRequest waitRequest(final Value input) {
        return InvokeWaitForMethodRequest.newBuilder()
                .setContext(context())
                .setFlowType("BridgeFlow")
                .setStepType("BridgeStep")
                .setStepInput(input)
                .build();
    }

    private static InvokeExecuteMethodRequest executeRequest(final Value input) {
        return InvokeExecuteMethodRequest.newBuilder()
                .setContext(context())
                .setFlowType("BridgeFlow")
                .setStepType("BridgeStep")
                .setStepInput(input)
                .build();
    }

    private static io.superdurable.gen.Context context() {
        return io.superdurable.gen.Context.newBuilder()
                .setFlowId("flow-1")
                .setRunId("run-1")
                .setFlowStartedTimestamp(1)
                .setStepExecutionId("step-1")
                .setFirstAttemptTimestamp(1)
                .setAttempt(1)
                .build();
    }

    private static Value concrete(final String value) {
        return Value.newBuilder().setStringValue(value).build();
    }

    private static void assertWorkerFailure(
            final RunningWorker running,
            final String input,
            final Class<? extends Throwable> expectedType) throws Exception {
        final StatusRuntimeException failure = assertThrows(
                StatusRuntimeException.class,
                () -> running.client.invokeExecuteMethod(executeRequest(concrete(input))));
        assertEquals(Status.Code.INTERNAL, failure.getStatus().getCode());
        final WorkerErrorResponse details = StatusProto.fromThrowable(failure)
                .getDetails(0)
                .unpack(WorkerErrorResponse.class);
        assertEquals(expectedType.getName(), details.getErrorType());
        assertTrue(details.getStackTrace().contains(expectedType.getName()));
    }

    private static BridgeFailureException largeFailure() {
        final BridgeFailureException failure = new BridgeFailureException("large bridge failure");
        for (int index = 0; index < 256; index++) {
            failure.addSuppressed(new IllegalStateException(
                    "suppressed failure " + index + " \u20ac"));
        }
        return failure;
    }

    @SuppressWarnings("unchecked")
    private static <Failure extends Throwable> void throwUnchecked(
            final Throwable failure) throws Failure {
        throw (Failure) failure;
    }

    private static int availablePort() throws IOException {
        try (ServerSocket socket = new ServerSocket(0)) {
            return socket.getLocalPort();
        }
    }

    private static final class RunningWorker implements AutoCloseable {
        private final Worker worker;
        private final Thread workerThread;
        private final AtomicReference<Throwable> workerFailure;
        private final ManagedChannel channel;
        private final WorkerServiceGrpc.WorkerServiceBlockingStub client;

        private RunningWorker(
                final Worker worker,
                final Thread workerThread,
                final AtomicReference<Throwable> workerFailure,
                final ManagedChannel channel,
                final WorkerServiceGrpc.WorkerServiceBlockingStub client) {
            this.worker = worker;
            this.workerThread = workerThread;
            this.workerFailure = workerFailure;
            this.channel = channel;
            this.client = client;
        }

        @Override
        public void close() throws InterruptedException {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            worker.close();
            workerThread.join(5_000L);
            assertNull(workerFailure.get());
        }
    }

    private static class BridgeFlow implements Flow<String> {
        private final BridgeStep start = new BridgeStep();

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(start);
        }

        @Override
        public String getFlowType() {
            return "BridgeFlow";
        }

        @RPC
        public RPCResult<String> checkedRpc(final Context context)
                throws CheckedBridgeException {
            throw new CheckedBridgeException("checked RPC failure");
        }
    }

    private static final class BridgeStep implements Step<String> {
        private final AtomicReference<String> handlerThread = new AtomicReference<String>();

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public String getStepType() {
            return "BridgeStep";
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            handlerThread.set(Thread.currentThread().getName());
            if ("fail".equals(input)) {
                throw new BridgeFailureException("bridge failed");
            }
            if ("checked".equals(input)) {
                WorkerServiceIntegrationTest.<RuntimeException>throwUnchecked(
                        new CheckedBridgeException("checked bridge failure"));
            }
            if ("error".equals(input)) {
                throw new AssertionError("bridge assertion failed");
            }
            if ("status".equals(input)) {
                throw Status.NOT_FOUND.withDescription("do not bypass mapping").asRuntimeException();
            }
            if ("checked-status".equals(input)) {
                WorkerServiceIntegrationTest.<RuntimeException>throwUnchecked(
                        Status.NOT_FOUND.withDescription("do not bypass mapping").asException());
            }
            if ("large".equals(input)) {
                throw largeFailure();
            }
            if ("invalid".equals(input)) {
                return StepDecision.goToMulti();
            }
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class BridgeFailureException extends RuntimeException {
        private BridgeFailureException(final String message) {
            super(message);
        }
    }

    private static final class CheckedBridgeException extends Exception {
        private CheckedBridgeException(final String message) {
            super(message);
        }
    }

    private static final class TestBlobCache implements BlobCache {
        private final Map<String, byte[]> values = new HashMap<String, byte[]>();

        @Override
        public synchronized Optional<byte[]> get(final String blobId) {
            final byte[] value = values.get(blobId);
            return value == null ? Optional.empty() : Optional.of(value.clone());
        }

        @Override
        public synchronized boolean put(final String blobId, final byte[] payload) {
            values.put(blobId, payload.clone());
            return true;
        }

        @Override
        public synchronized void delete(final String blobId) {
            values.remove(blobId);
        }

        @Override
        public synchronized void deleteAll() {
            values.clear();
        }

        @Override
        public void close() {
        }
    }
}
