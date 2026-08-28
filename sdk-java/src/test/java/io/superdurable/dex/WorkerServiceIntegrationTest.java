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

import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.common.util.concurrent.ListenableFuture;
import com.google.protobuf.Empty;
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
import io.superdurable.dex.exceptions.RetryAfterException;
import io.superdurable.gen.CloseDecisionType;
import io.superdurable.gen.ChannelInfo;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.InvokeExecuteMethodRequest;
import io.superdurable.gen.InvokeExecuteMethodResponse;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.InvokeWorkerRPCRequest;
import io.superdurable.gen.InvokeWorkerRPCResponse;
import io.superdurable.gen.KV;
import io.superdurable.gen.LoadBlobsRequest;
import io.superdurable.gen.LoadBlobsResponse;
import io.superdurable.gen.SyncAttributeIndexRequest;
import io.superdurable.gen.SyncAttributeIndexResponse;
import io.superdurable.gen.Value;
import io.superdurable.gen.WorkerServiceGrpc;
import io.superdurable.gen.WorkerErrorResponse;
import io.superdurable.gen.WriteStreamRequest;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Arrays;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CountDownLatch;
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
            assertEquals(
                    io.superdurable.gen.IndexType.INDEX_TYPE_KEYWORD,
                    running.syncRequest.get().getAttributeIndexesOrThrow("JavaWorkerStatus"));
            assertFalse(running.listeningDuringSync.get());
        } finally {
            running.close();
        }
    }

    @Test
    void stepStreamWritesUseExecutionIdempotencyKey() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            running.client.invokeExecuteMethod(executeRequest(concrete("stream")));
            final WriteStreamRequest request = running.writeStreamRequest.get();
            assertEquals("flow-1", request.getFlowId());
            assertEquals("BridgeFlow", request.getFlowType());
            assertEquals("thinking", request.getStreamName());
            assertEquals(1_048_576, request.getStreamCapacityBytes());
            assertEquals("run-1#step-1", request.getIdempotencyKey());
            assertEquals("stream", request.getValue().getStringValue());
        } finally {
            running.close();
        }
    }

    @Test
    void mapsConditionIdsWithoutInternalValues() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            final InvokeWaitForMethodResponse unnamed = running.client.invokeWaitForMethod(
                    waitRequest(concrete("unnamed")));
            assertEquals("", unnamed.getWaitingCondition()
                    .getTimerConditions(0).getConditionId());
            assertEquals("", unnamed.getWaitingCondition()
                    .getChannelConditions(0).getConditionId());

            final InvokeWaitForMethodResponse reused = running.client.invokeWaitForMethod(
                    waitRequest(concrete("reused")));
            assertEquals(
                    "__dex_internal_condition_0",
                    reused.getWaitingCondition().getChannelConditions(0).getConditionId());
            assertEquals(
                    reused.getWaitingCondition().getConditionCombinations(0).getConditionIds(0),
                    reused.getWaitingCondition().getConditionCombinations(1).getConditionIds(0));

            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeWaitForMethod(
                            waitRequest(concrete("missing-combination-id"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeWaitForMethod(
                            waitRequest(concrete("duplicate-id"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeWaitForMethod(
                            waitRequest(concrete("empty-id"))));
        } finally {
            running.close();
        }
    }

    @Test
    void mapIntrospectionTracksBufferedChanges() {
        final AttributeMap<String> attributes = AttributeMap.define("items", String.class);
        final ChannelMap<String> channels = ChannelMap.define("messages", String.class);
        final Flow<Void> flow = new Flow<Void>() {
            @Override
            public String getFlowType() {
                return "MapFlow";
            }

            @Override
            public StepList<Void> getSteps() {
                return StepList.empty();
            }

            @Override
            public PersistenceSchema getPersistenceSchema() {
                return PersistenceSchema.of(attributes, channels);
            }
        };
        final Registry registry = new Registry(Collections.<Flow<?>>singletonList(flow));
        final ValueMapper values = new ValueMapper(new ObjectMapper());
        final String special = "special / key";
        final Map<String, ChannelInfo> channelInfos = new HashMap<String, ChannelInfo>();
        channelInfos.put(
                Registry.physicalName("messages", special),
                ChannelInfo.newBuilder().setSize(1).build());
        channelInfos.put(
                Registry.physicalName("messages", "empty"),
                ChannelInfo.getDefaultInstance());
        final InvocationContext context = new InvocationContext(
                InvocationContext.Method.RPC,
                registry.getFlow("MapFlow"),
                io.superdurable.gen.Context.getDefaultInstance(),
                values,
                null,
                Arrays.asList(
                        KV.newBuilder()
                                .setKey(Registry.physicalName("items", special))
                                .setValue(values.encode("initial"))
                                .build(),
                        KV.newBuilder()
                                .setKey(Registry.physicalName("items", "z"))
                                .setValue(values.encode("remove"))
                                .build()),
                Collections.<KV>emptyList(),
                null,
                channelInfos);
        assertEquals(Arrays.asList(special, "z"), attributes.getAllInstanceKeys(context));
        attributes.set(context, "a", "added");
        attributes.delete(context, "z");
        assertEquals(Arrays.asList("a", special), attributes.getAllInstanceKeys(context));
        assertEquals(2, attributes.getMapSize(context));
        assertEquals(Collections.singletonList(special), channels.getAllInstanceKeys(context));
        channels.publish(context, "a", "published");
        assertEquals(Arrays.asList("a", special), channels.getAllInstanceKeys(context));
        assertEquals(2, channels.getMapSize(context));
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
    void preservesRetryAfterAndOriginalFailure() throws Exception {
        final RunningWorker running = startWorker(new BridgeFlow(), new TestBlobCache(), null);
        try {
            final StatusRuntimeException failure = assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(
                            executeRequest(concrete("retry-after"))));
            final WorkerErrorResponse details = StatusProto.fromThrowable(failure)
                    .getDetails(0)
                    .unpack(WorkerErrorResponse.class);
            assertEquals(BridgeFailureException.class.getName(), details.getErrorType());
            assertEquals("retry later", details.getDetail());
            assertEquals(7, details.getRetryAfterSeconds());
            assertTrue(details.getStackTrace().contains(BridgeFailureException.class.getName()));
        } finally {
            running.close();
        }
    }

    @Test
    void validatesRetryAfter() {
        final RuntimeException currentCause = new RuntimeException("failure");
        assertThrows(
                IllegalArgumentException.class,
                () -> RetryAfterException.after(null, currentCause));
        assertThrows(
                IllegalArgumentException.class,
                () -> RetryAfterException.after(Duration.ZERO, currentCause));
        assertThrows(
                IllegalArgumentException.class,
                () -> RetryAfterException.after(Duration.ofSeconds(-1), currentCause));
        assertThrows(
                IllegalArgumentException.class,
                () -> RetryAfterException.after(Duration.ofMillis(1500), currentCause));
        assertThrows(
                IllegalArgumentException.class,
                () -> RetryAfterException.after(
                        Duration.ofSeconds((long) Integer.MAX_VALUE + 1), currentCause));
        assertThrows(
                IllegalArgumentException.class,
                () -> RetryAfterException.after(Duration.ofSeconds(1), null));
    }

    @Test
    void exposesRecoveryErrorFromContext() {
        final InvocationContext context = new InvocationContext(
                InvocationContext.Method.EXECUTE,
                new Registry(Collections.<Flow<?>>singletonList(new BridgeFlow()))
                        .getFlow("BridgeFlow"),
                io.superdurable.gen.Context.newBuilder()
                        .setRecoveryError(io.superdurable.gen.RecoveryErrorInfo.newBuilder()
								.setDetail("worker detail")
								.setErrorType("worker type"))
                        .build(),
                new ValueMapper(new ObjectMapper()),
                null,
                Collections.<KV>emptyList(),
                Collections.<KV>emptyList(),
                null,
                Collections.<String, ChannelInfo>emptyMap());

		final RecoveryErrorInfo recoveryError = context.getRecoveryError();
		assertEquals("worker detail", recoveryError.getDetail());
		assertEquals("worker type", recoveryError.getErrorType());
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
    void mapsImmutableCancellationSelectorsAndHeartbeatTimeout() throws Exception {
        final BridgeFlow flow = new BridgeFlow();
        final RunningWorker running = startWorker(flow, new TestBlobCache(), null);
        try {
            final InvokeExecuteMethodResponse cancellation = running.client.invokeExecuteMethod(
                    executeRequest(concrete("cancel")));
            assertEquals(
                    Collections.singletonList("BridgeOtherStep"),
                    cancellation.getStepDecision().getCancelStepTypesList());
            assertEquals(
                    Collections.singletonList("BridgeStep"),
                    cancellation.getStepDecision().getCancelSiblingStepTypesList());
            assertTrue(flow.start.baseDecision.get().getCancelingSteps().isEmpty());
            assertTrue(flow.start.baseDecision.get().getCancelingSiblingSteps().isEmpty());

            final InvokeWorkerRPCResponse rpcCancellation = running.client.invokeWorkerRPC(
                    rpcRequest("cancel"));
            assertEquals(
                    Collections.singletonList("BridgeOtherStep"),
                    rpcCancellation.getStepDecision().getCancelStepTypesList());
            assertTrue(rpcCancellation.getStepDecision().getCancelSiblingStepTypesList().isEmpty());
            assertEquals(
                    "BridgeStep",
                    rpcCancellation.getStepDecision().getNextSteps(0).getStepType());
            assertTrue(flow.baseRpcResult.get().getCancelingSteps().isEmpty());

            final InvokeExecuteMethodResponse heartbeat = running.client.invokeExecuteMethod(
                    executeRequest(concrete("heartbeat")));
            assertEquals(
                    10,
                    heartbeat.getStepDecision()
                            .getNextSteps(0)
                            .getStepOptions()
                            .getHeartbeatTimeoutSeconds());

            final InvokeExecuteMethodResponse disabled = running.client.invokeExecuteMethod(
                    executeRequest(concrete("heartbeat-zero")));
            assertEquals(
                    0,
                    disabled.getStepDecision()
                            .getNextSteps(0)
                            .getStepOptions()
                            .getHeartbeatTimeoutSeconds());

            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(
                            executeRequest(concrete("cancel-foreign"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(
                            executeRequest(concrete("cancel-null"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(
                            executeRequest(concrete("heartbeat-fraction"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(
                            executeRequest(concrete("heartbeat-negative"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeExecuteMethod(
                            executeRequest(concrete("heartbeat-overflow"))));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeWorkerRPC(rpcRequest("cancel-foreign")));
            assertThrows(
                    StatusRuntimeException.class,
                    () -> running.client.invokeWorkerRPC(rpcRequest("cancel-null")));
        } finally {
            running.close();
        }
    }

    @Test
    void grpcCancellationInterruptsHandlerAndUpdatesContext() throws Exception {
        final BridgeFlow flow = new BridgeFlow();
        final RunningWorker running = startWorker(flow, new TestBlobCache(), null);
        try {
            final ListenableFuture<InvokeExecuteMethodResponse> response =
                    WorkerServiceGrpc.newFutureStub(running.channel)
                            .withWaitForReady()
                            .withDeadlineAfter(10, TimeUnit.SECONDS)
                            .invokeExecuteMethod(executeRequest(concrete("block")));
            assertTrue(flow.start.blockStarted.await(5, TimeUnit.SECONDS));
            response.cancel(true);
            assertTrue(flow.start.cancellationObserved.await(5, TimeUnit.SECONDS));
            assertTrue(flow.start.contextReportedCancellation.get());
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
                    public void syncAttributeIndexes(
                            final SyncAttributeIndexRequest request,
                            final StreamObserver<SyncAttributeIndexResponse> observer) {
                        observer.onNext(SyncAttributeIndexResponse.getDefaultInstance());
                        observer.onCompleted();
                    }

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
        final AtomicReference<SyncAttributeIndexRequest> syncRequest =
                new AtomicReference<SyncAttributeIndexRequest>();
        final AtomicReference<Boolean> listeningDuringSync =
                new AtomicReference<Boolean>();
        final AtomicReference<WriteStreamRequest> writeStreamRequest =
                new AtomicReference<WriteStreamRequest>();
        final Server ownedFlowServer;
        final String effectiveServerAddress;
        if (serverAddress == null) {
            final int flowPort = availablePort();
            ownedFlowServer = ServerBuilder.forPort(flowPort)
                    .addService(new FlowServiceGrpc.FlowServiceImplBase() {
                        @Override
                        public void syncAttributeIndexes(
                                final SyncAttributeIndexRequest request,
                                final StreamObserver<SyncAttributeIndexResponse> observer) {
                            syncRequest.set(request);
                            try (Socket socket = new Socket()) {
                                socket.connect(new InetSocketAddress("127.0.0.1", port), 100);
                                listeningDuringSync.set(Boolean.TRUE);
                            } catch (IOException expected) {
                                listeningDuringSync.set(Boolean.FALSE);
                            }
                            observer.onNext(SyncAttributeIndexResponse.getDefaultInstance());
                            observer.onCompleted();
                        }

                        @Override
                        public void writeStream(
                                final WriteStreamRequest request,
                                final StreamObserver<Empty> observer) {
                            writeStreamRequest.set(request);
                            observer.onNext(Empty.getDefaultInstance());
                            observer.onCompleted();
                        }
                    })
                    .build()
                    .start();
            effectiveServerAddress = "127.0.0.1:" + flowPort;
        } else {
            ownedFlowServer = null;
            effectiveServerAddress = serverAddress;
        }
        final WorkerOptions.Builder options = WorkerOptions.newBuilder()
                .bindAddress("127.0.0.1:" + port)
                .serverAddress(effectiveServerAddress);
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
        return new RunningWorker(
                worker,
                workerThread,
                workerFailure,
                channel,
                client,
                ownedFlowServer,
                syncRequest,
                listeningDuringSync,
                writeStreamRequest);
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

    private static InvokeWorkerRPCRequest rpcRequest(final String input) {
        return InvokeWorkerRPCRequest.newBuilder()
                .setContext(context())
                .setFlowType("BridgeFlow")
                .setRpcName("cancellationRpc")
                .setInput(concrete(input))
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
        private final Server ownedFlowServer;
        private final AtomicReference<SyncAttributeIndexRequest> syncRequest;
        private final AtomicReference<Boolean> listeningDuringSync;
        private final AtomicReference<WriteStreamRequest> writeStreamRequest;

        private RunningWorker(
                final Worker worker,
                final Thread workerThread,
                final AtomicReference<Throwable> workerFailure,
                final ManagedChannel channel,
                final WorkerServiceGrpc.WorkerServiceBlockingStub client,
                final Server ownedFlowServer,
                final AtomicReference<SyncAttributeIndexRequest> syncRequest,
                final AtomicReference<Boolean> listeningDuringSync,
                final AtomicReference<WriteStreamRequest> writeStreamRequest) {
            this.worker = worker;
            this.workerThread = workerThread;
            this.workerFailure = workerFailure;
            this.channel = channel;
            this.client = client;
            this.ownedFlowServer = ownedFlowServer;
            this.syncRequest = syncRequest;
            this.listeningDuringSync = listeningDuringSync;
            this.writeStreamRequest = writeStreamRequest;
        }

        @Override
        public void close() throws InterruptedException {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            worker.close();
            workerThread.join(5_000L);
            assertNull(workerFailure.get());
            if (ownedFlowServer != null) {
                ownedFlowServer.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            }
        }
    }

    private static class BridgeFlow implements Flow<String> {
        private final Channel<Void> commands = Channel.define("commands", Void.class);
        private final Stream<String> thinking =
                Stream.define("thinking", String.class, 1_048_576);
        private final BridgeOtherStep other = new BridgeOtherStep();
        private final BridgeStep start = new BridgeStep(commands, thinking);
        private final Attribute<String> status = Attribute.define(
                "status",
                String.class,
                new AttributeIndex(AttributeIndex.Type.KEYWORD, "JavaWorkerStatus"));
        private final AtomicReference<RPCResult<String>> baseRpcResult =
                new AtomicReference<RPCResult<String>>();

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(start).otherSteps(other);
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

        @RPC
        public RPCResult<String> cancellationRpc(final Context context, final String input) {
            if ("cancel-foreign".equals(input)) {
                return RPCResult.of(input)
                        .withCancelingSteps(ForeignBridgeStep.class);
            }
            if ("cancel-null".equals(input)) {
                return RPCResult.of(input)
                        .withCancelingSteps((Class<? extends Step<?>>[]) null);
            }
            final RPCResult<String> base = RPCResult.of(
                    input,
                    StepMovement.of(BridgeStep.class, "next"));
            baseRpcResult.set(base);
            return base.withCancelingSteps(BridgeOtherStep.class, BridgeOtherStep.class)
                    .withCancelingSteps();
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(status, commands, thinking);
        }
    }

    private static final class BridgeStep implements Step<String> {
        private final AtomicReference<String> handlerThread = new AtomicReference<String>();
        private final AtomicReference<StepDecision> baseDecision =
                new AtomicReference<StepDecision>();
        private final AtomicReference<Boolean> contextReportedCancellation =
                new AtomicReference<Boolean>(Boolean.FALSE);
        private final CountDownLatch blockStarted = new CountDownLatch(1);
        private final CountDownLatch cancellationObserved = new CountDownLatch(1);
        private final Channel<Void> commands;
        private final Stream<String> thinking;

        private BridgeStep(
                final Channel<Void> commands,
                final Stream<String> thinking) {
            this.commands = commands;
            this.thinking = thinking;
        }

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
            if ("unnamed".equals(input)) {
                return Wait.allOf(
                        Timer.byDuration(java.time.Duration.ofSeconds(1)),
                        commands.forOne());
            }
            if ("reused".equals(input)) {
                final Condition reused = commands.forOne("__dex_internal_condition_0");
                return Wait.anyCombinationOf(
                        ConditionCombination.of(reused),
                        ConditionCombination.of(reused));
            }
            if ("missing-combination-id".equals(input)) {
                return Wait.anyCombinationOf(ConditionCombination.of(commands.forOne()));
            }
            if ("duplicate-id".equals(input)) {
                return Wait.anyCombinationOf(ConditionCombination.of(
                        commands.forOne("duplicate"),
                        Timer.byDuration(java.time.Duration.ofSeconds(1), "duplicate")));
            }
            if ("empty-id".equals(input)) {
                return Wait.allOf(commands.forOne(""));
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            if ("stream".equals(input)) {
                thinking.write(context, input);
            }
            handlerThread.set(Thread.currentThread().getName());
            if ("fail".equals(input)) {
                throw new BridgeFailureException("bridge failed");
            }
            if ("retry-after".equals(input)) {
                throw RetryAfterException.after(
                        Duration.ofSeconds(7),
                        new BridgeFailureException("retry later"));
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
            if ("cancel".equals(input)) {
                final StepDecision base = StepDecision.gracefulComplete(input);
                baseDecision.set(base);
                return base.withCancelingSiblingSteps(
                                BridgeOtherStep.class, BridgeStep.class, BridgeOtherStep.class)
                        .withCancelingSteps(BridgeOtherStep.class, BridgeOtherStep.class)
                        .withCancelingSteps()
                        .withCancelingSiblingSteps();
            }
            if ("cancel-foreign".equals(input)) {
                return StepDecision.gracefulComplete()
                        .withCancelingSteps(ForeignBridgeStep.class);
            }
            if ("cancel-null".equals(input)) {
                return StepDecision.gracefulComplete()
                        .withCancelingSteps((Class<? extends Step<?>>[]) null);
            }
            if ("heartbeat".equals(input)) {
                return heartbeatDecision(Duration.ofSeconds(10));
            }
            if ("heartbeat-zero".equals(input)) {
                return heartbeatDecision(Duration.ZERO);
            }
            if ("heartbeat-fraction".equals(input)) {
                return heartbeatDecision(Duration.ofMillis(1500));
            }
            if ("heartbeat-negative".equals(input)) {
                return heartbeatDecision(Duration.ofSeconds(-1));
            }
            if ("heartbeat-overflow".equals(input)) {
                return heartbeatDecision(Duration.ofSeconds((long) Integer.MAX_VALUE + 1));
            }
            if ("block".equals(input)) {
                blockStarted.countDown();
                try {
                    Thread.sleep(TimeUnit.MINUTES.toMillis(1));
                } catch (InterruptedException canceled) {
                    contextReportedCancellation.set(context.isCancellationRequested());
                    cancellationObserved.countDown();
                    Thread.currentThread().interrupt();
                }
            }
            return StepDecision.gracefulComplete(input);
        }

        private StepDecision heartbeatDecision(final Duration timeout) {
            return StepDecision.goToMulti(StepMovement.of(
                    BridgeOtherStep.class,
                    "next",
                    StepOptions.newBuilder().heartbeatTimeout(timeout).build()));
        }
    }

    private static final class BridgeOtherStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.deadEnd();
        }
    }

    private static final class ForeignBridgeStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.deadEnd();
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
