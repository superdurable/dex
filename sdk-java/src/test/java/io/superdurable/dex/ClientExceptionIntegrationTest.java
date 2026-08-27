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

import com.google.protobuf.Any;
import com.google.protobuf.ByteString;
import com.google.protobuf.Empty;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.StreamObserver;
import io.superdurable.dex.exceptions.DexServiceException;
import io.superdurable.dex.exceptions.ErrorSubStatus;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.exceptions.RpcLockConflictException;
import io.superdurable.dex.exceptions.WorkerInvocationException;
import io.superdurable.gen.ServiceErrorResponse;
import io.superdurable.gen.EncodedObject;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetFlowSummaryRequest;
import io.superdurable.gen.GetFlowSummaryResponse;
import io.superdurable.gen.LoadBlobsRequest;
import io.superdurable.gen.LoadBlobsResponse;
import io.superdurable.gen.ReadStreamRequest;
import io.superdurable.gen.ReadStreamResponse;
import io.superdurable.gen.StopFlowRequest;
import io.superdurable.gen.StepCompletionOutput;
import io.superdurable.gen.Value;
import io.superdurable.gen.WaitForFlowRequest;
import io.superdurable.gen.WaitForStepCompletionRequest;
import io.superdurable.gen.WaitForStepCompletionResponse;
import io.superdurable.gen.WriteStreamRequest;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.time.Duration;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class ClientExceptionIntegrationTest {
    private static final Stream<String> THINKING =
            Stream.define("thinking", String.class, 1_048_576);
    private Server server;
    private Client client;
    private ErrorFlowService flowService;

    @BeforeEach
    void startServer() throws Exception {
        flowService = new ErrorFlowService();
        server = ServerBuilder.forPort(0)
                .addService(flowService)
                .build()
                .start();
        client = new Client(
                new Registry(Collections.<Flow<?>>singletonList(new StreamFlow())),
                new TestBlobCache(),
                new ClientOptions("127.0.0.1:" + server.getPort()));
    }

    @AfterEach
    void stopServer() throws Exception {
        client.close();
        server.shutdownNow();
        server.awaitTermination(5, TimeUnit.SECONDS);
    }

    @Test
    void mapsMissingFlowByEndpointRequirement() {
        final FlowNotFoundException missing = assertThrows(
                FlowNotFoundException.class,
                () -> client.describeFlow("missing"));
        assertEquals(Status.Code.NOT_FOUND, missing.getCode());
        assertEquals(ErrorSubStatus.FLOW_NOT_EXISTS, missing.getSubStatus());

        final FlowNotActiveException inactive = assertThrows(
                FlowNotActiveException.class,
                () -> client.stopFlow("inactive"));
        assertEquals(Status.Code.NOT_FOUND, inactive.getCode());
        assertEquals(ErrorSubStatus.FLOW_NOT_EXISTS, inactive.getSubStatus());
    }

    @Test
    void distinguishesWorkerInvocationFromRpcLockConflict() {
        final WorkerInvocationException worker = assertThrows(
                WorkerInvocationException.class,
                () -> client.stopFlow("worker"));
        assertEquals(Status.Code.FAILED_PRECONDITION, worker.getCode());
        assertEquals(Status.Code.UNKNOWN, worker.getWorkerCode());
        assertEquals("example.WorkerFailure", worker.getWorkerErrorType());
        assertEquals("handler failed", worker.getWorkerErrorDetail());
        assertEquals("example.WorkerFailure: handler failed", worker.getWorkerStackTrace());

        final RpcLockConflictException conflict = assertThrows(
                RpcLockConflictException.class,
                () -> client.stopFlow("lock"));
        assertEquals(Status.Code.ABORTED, conflict.getCode());
    }

    @Test
    void mapsLongPollTimeoutAcrossWaitOperations() {
        final LongPollTimeoutException flowTimeout = assertThrows(
                LongPollTimeoutException.class,
                () -> client.waitForFlow("timeout", Duration.ofSeconds(1)).getSingleOutput(Void.class));
        assertEquals("timeout", flowTimeout.getFlowId());
        assertEquals(Status.Code.DEADLINE_EXCEEDED, flowTimeout.getCode());

        final LongPollTimeoutException stepTimeout = assertThrows(
                LongPollTimeoutException.class,
                () -> client.waitForStepCompletion(
                        "step-timeout",
                        StepExecutionId.of("WaitingStep", 1),
                        Duration.ofSeconds(1)));
        assertEquals("step-timeout", stepTimeout.getFlowId());
        assertEquals(Status.Code.DEADLINE_EXCEEDED, stepTimeout.getCode());
    }

    @Test
    void returnsEveryStepCompletionAndRejectsAmbiguousSingleOutput() {
        final FlowResult result = client.waitForFlow("multi", Duration.ofSeconds(1));

        assertEquals(2, result.getCompletions().size());
        assertEquals("First", result.getCompletions().get(0).getStepType());
        assertEquals("First-1", result.getCompletions().get(0).getStepExecutionId());
        assertEquals("one", result.getCompletions().get(0).getOutput(String.class));
        assertArrayEquals(
                ByteString.copyFromUtf8("done").toByteArray(),
                result.getCompletions().get(1).getOutput(byte[].class));
        assertThrows(IllegalStateException.class, () -> result.getSingleOutput(String.class));
        assertEquals(
                "one",
                client.waitForFlow("single", Duration.ofSeconds(1))
                        .getSingleOutput(String.class));
        assertThrows(
                IllegalStateException.class,
                () -> client.waitForFlow("empty", Duration.ofSeconds(1))
                        .getSingleOutput(String.class));

        final FlowResult failure = client.waitForFlow("failed", Duration.ofSeconds(1));
        assertEquals("Second-2", failure.getCompletions().get(1).getStepExecutionId());
        assertArrayEquals(
                ByteString.copyFromUtf8("done").toByteArray(),
                failure.getCompletions().get(1).getOutput(byte[].class));
    }

    @Test
    void mapsStreamTransportAndMetadata() {
        client.writeStream("flow-1", THINKING, "client-1", "starting");
        final StreamMessage<String> message = client.readStream(
                "flow-1",
                THINKING,
                "previous",
                Duration.ofSeconds(2));

        assertEquals("StreamFlow", flowService.writeStreamRequest.getFlowType());
        assertEquals("thinking", flowService.writeStreamRequest.getStreamName());
        assertEquals(1_048_576, flowService.writeStreamRequest.getMaxEstimatedBytes());
        assertEquals("client-1", flowService.writeStreamRequest.getIdempotencyKey());
        assertEquals("previous", flowService.readStreamRequest.getResumeToken());
        assertEquals(2, flowService.readStreamRequest.getWaitTimeSeconds());
        assertEquals("working", message.getValue());
        assertEquals("resume-1", message.getResumeToken());
        assertEquals(java.time.Instant.parse("2026-08-27T12:00:00Z"), message.getCreatedTime());
        assertEquals("client-1", message.getIdempotencyKey());
        assertThrows(
                IllegalArgumentException.class,
                () -> client.writeStream("flow-1", THINKING, "bad#key", "ignored"));
    }

    @Test
    void fallsBackForMissingUnknownAndMalformedDetails() {
        final DexServiceException missingDetails = assertThrows(
                DexServiceException.class,
                () -> client.describeFlow("no-details"));
        assertEquals(ErrorSubStatus.UNCATEGORIZED, missingDetails.getSubStatus());
        assertEquals("plain failure", missingDetails.getDetail());

        final DexServiceException unknown = assertThrows(
                DexServiceException.class,
                () -> client.describeFlow("unknown"));
        assertEquals(ErrorSubStatus.UNCATEGORIZED, unknown.getSubStatus());

        final DexServiceException malformed = assertThrows(
                DexServiceException.class,
                () -> client.describeFlow("malformed"));
        assertEquals(ErrorSubStatus.UNCATEGORIZED, malformed.getSubStatus());
        assertTrue(malformed.getDetail().contains("malformed"));
        assertEquals(1, malformed.getSuppressed().length);
    }

    private static StatusRuntimeException error(
            final Status.Code code,
            final io.superdurable.gen.ErrorSubStatus subStatus,
            final String detail) {
        return error(code, ServiceErrorResponse.newBuilder()
                .setSubStatus(subStatus)
                .setDetail(detail)
                .build());
    }

    private static StatusRuntimeException error(
            final Status.Code code,
            final ServiceErrorResponse response) {
        return StatusProto.toStatusRuntimeException(com.google.rpc.Status.newBuilder()
                .setCode(code.value())
                .setMessage(response.getDetail())
                .addDetails(Any.pack(response))
                .build());
    }

    private static final class ErrorFlowService
            extends FlowServiceGrpc.FlowServiceImplBase {
        private WriteStreamRequest writeStreamRequest;
        private ReadStreamRequest readStreamRequest;

        @Override
        public void writeStream(
                final WriteStreamRequest request,
                final StreamObserver<Empty> observer) {
            writeStreamRequest = request;
            observer.onNext(Empty.getDefaultInstance());
            observer.onCompleted();
        }

        @Override
        public void readStream(
                final ReadStreamRequest request,
                final StreamObserver<ReadStreamResponse> observer) {
            readStreamRequest = request;
            observer.onNext(ReadStreamResponse.newBuilder()
                    .setMessage(io.superdurable.gen.StreamMessage.newBuilder()
                            .setValue(Value.newBuilder().setStringValue("working"))
                            .setResumeToken("resume-1")
                            .setCreatedTime(com.google.protobuf.Timestamp.newBuilder()
                                    .setSeconds(1_787_832_000L))
                            .setIdempotencyKey("client-1"))
                    .build());
            observer.onCompleted();
        }

        @Override
        public void getFlowSummary(
                final GetFlowSummaryRequest request,
                final StreamObserver<GetFlowSummaryResponse> observer) {
            if ("failed".equals(request.getFlowId())) {
                observer.onNext(GetFlowSummaryResponse.newBuilder()
                        .setFlowExecutionId(io.superdurable.gen.FlowExecutionID.newBuilder()
                                .setFlowId("failed")
                                .setRunId("run-failed"))
                        .setFlowStatus(io.superdurable.gen.FlowStatus.FLOW_STATUS_FAILED)
                        .build());
                observer.onCompleted();
                return;
            }
            if ("no-details".equals(request.getFlowId())) {
                observer.onError(Status.NOT_FOUND
                        .withDescription("plain failure")
                        .asRuntimeException());
                return;
            }
            if ("unknown".equals(request.getFlowId())) {
                observer.onError(error(
                        Status.Code.NOT_FOUND,
                        ServiceErrorResponse.newBuilder()
                                .setSubStatusValue(999)
                                .setDetail("unknown sub-status")
                                .build()));
                return;
            }
            if ("malformed".equals(request.getFlowId())) {
                final Any malformed = Any.newBuilder()
                        .setTypeUrl("type.googleapis.com/dex.ServiceErrorResponse")
                        .setValue(ByteString.copyFromUtf8("malformed"))
                        .build();
                observer.onError(StatusProto.toStatusRuntimeException(
                        com.google.rpc.Status.newBuilder()
                                .setCode(Status.Code.NOT_FOUND.value())
                                .setMessage("malformed details")
                                .addDetails(malformed)
                                .build()));
                return;
            }
            observer.onError(error(
                    Status.Code.NOT_FOUND,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
                    "flow is missing"));
        }

        @Override
        public void stopFlow(
                final StopFlowRequest request,
                final StreamObserver<Empty> observer) {
            if ("worker".equals(request.getFlowId())) {
                observer.onError(error(
                        Status.Code.FAILED_PRECONDITION,
                        ServiceErrorResponse.newBuilder()
                                .setSubStatus(io.superdurable.gen.ErrorSubStatus
                                        .ERROR_SUB_STATUS_WORKER_API_ERROR)
                                .setDetail("worker invocation failed")
                                .setOriginalWorkerErrorType("example.WorkerFailure")
                                .setOriginalWorkerErrorDetail("handler failed")
                                .setOriginalWorkerErrorStackTrace(
                                        "example.WorkerFailure: handler failed")
                                .setOriginalWorkerErrorStatus(Status.Code.UNKNOWN.value())
                                .build()));
                return;
            }
            if ("lock".equals(request.getFlowId())) {
                observer.onError(error(
                        Status.Code.ABORTED,
                        io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_WORKER_API_ERROR,
                        "RPC lock conflict"));
                return;
            }
            observer.onError(error(
                    Status.Code.NOT_FOUND,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS,
                    "flow is not active"));
        }

        @Override
        public void waitForFlow(
                final WaitForFlowRequest request,
                final StreamObserver<io.superdurable.gen.FlowResult> observer) {
            if ("multi".equals(request.getFlowId())
                    || "single".equals(request.getFlowId())
                    || "empty".equals(request.getFlowId())
                    || "failed".equals(request.getFlowId())) {
                final io.superdurable.gen.FlowResult.Builder response =
                        io.superdurable.gen.FlowResult.newBuilder()
                                .setFlowStatus("failed".equals(request.getFlowId())
                                        ? io.superdurable.gen.FlowStatus.FLOW_STATUS_FAILED
                                        : io.superdurable.gen.FlowStatus.FLOW_STATUS_COMPLETED);
                if (!"empty".equals(request.getFlowId())) {
                    response.addResults(StepCompletionOutput.newBuilder()
                            .setCompletedStepType("First")
                            .setCompletedStepExecutionId("First-1")
                            .setCompletedStepOutput(Value.newBuilder()
                                    .setInternalBlobIdForStringValue("first-blob")));
                }
                if ("multi".equals(request.getFlowId()) || "failed".equals(request.getFlowId())) {
                    response.addResults(StepCompletionOutput.newBuilder()
                            .setCompletedStepType("Second")
                            .setCompletedStepExecutionId("Second-2")
                            .setCompletedStepOutput(Value.newBuilder()
                                    .setInternalBlobIdForObjValue("second-blob")));
                }
                observer.onNext(response.build());
                observer.onCompleted();
                return;
            }
            observer.onError(error(
                    Status.Code.DEADLINE_EXCEEDED,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
                    "long poll timed out"));
        }

        @Override
        public void loadBlobs(
                final LoadBlobsRequest request,
                final StreamObserver<LoadBlobsResponse> observer) {
            final LoadBlobsResponse.Builder response = LoadBlobsResponse.newBuilder();
            for (final Value value : request.getValuesList()) {
                if (value.hasInternalBlobIdForStringValue()) {
                    response.putValues(
                            value.getInternalBlobIdForStringValue(),
                            Value.newBuilder().setStringValue("one").build());
                } else {
                    response.putValues(
                            value.getInternalBlobIdForObjValue(),
                            Value.newBuilder()
                                    .setObjValue(EncodedObject.newBuilder()
                                            .setEncoding("rawbytes")
                                            .setPayload(ByteString.copyFromUtf8("done")))
                                    .build());
                }
            }
            observer.onNext(response.build());
            observer.onCompleted();
        }

        @Override
        public void waitForStepCompletion(
                final WaitForStepCompletionRequest request,
                final StreamObserver<WaitForStepCompletionResponse> observer) {
            observer.onError(error(
                    Status.Code.DEADLINE_EXCEEDED,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
                    "long poll timed out"));
        }
    }

    private static final class StreamFlow implements Flow<String> {
        @Override
        public String getFlowType() {
            return "StreamFlow";
        }

        @Override
        public StepList<String> getSteps() {
            return StepList.empty();
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(THINKING);
        }
    }

    private static final class TestBlobCache implements BlobCache {
        private final Map<String, byte[]> values = new HashMap<String, byte[]>();

        @Override
        public Optional<byte[]> get(final String blobId) {
            return Optional.ofNullable(values.get(blobId));
        }

        @Override
        public boolean put(final String blobId, final byte[] payload) {
            values.put(blobId, payload);
            return true;
        }

        @Override
        public void delete(final String blobId) {
            values.remove(blobId);
        }

        @Override
        public void deleteAll() {
            values.clear();
        }

        @Override
        public void close() {
        }
    }
}
