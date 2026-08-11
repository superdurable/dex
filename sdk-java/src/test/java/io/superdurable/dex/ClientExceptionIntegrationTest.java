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
import io.superdurable.gen.ErrorResponse;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetFlowSummaryRequest;
import io.superdurable.gen.GetFlowSummaryResponse;
import io.superdurable.gen.StopFlowRequest;
import io.superdurable.gen.WaitForFlowRequest;
import io.superdurable.gen.WaitForFlowResponse;
import io.superdurable.gen.WaitForStepCompletionRequest;
import io.superdurable.gen.WaitForStepCompletionResponse;
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
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class ClientExceptionIntegrationTest {
    private Server server;
    private Client client;

    @BeforeEach
    void startServer() throws Exception {
        server = ServerBuilder.forPort(0)
                .addService(new ErrorFlowService())
                .build()
                .start();
        client = new Client(
                new Registry(Collections.<Flow<?>>emptyList()),
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
                () -> client.waitForFlow("timeout", Void.class, Duration.ofSeconds(1)));
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
        return error(code, ErrorResponse.newBuilder()
                .setSubStatus(subStatus)
                .setDetail(detail)
                .build());
    }

    private static StatusRuntimeException error(
            final Status.Code code,
            final ErrorResponse response) {
        return StatusProto.toStatusRuntimeException(com.google.rpc.Status.newBuilder()
                .setCode(code.value())
                .setMessage(response.getDetail())
                .addDetails(Any.pack(response))
                .build());
    }

    private static final class ErrorFlowService
            extends FlowServiceGrpc.FlowServiceImplBase {
        @Override
        public void getFlowSummary(
                final GetFlowSummaryRequest request,
                final StreamObserver<GetFlowSummaryResponse> observer) {
            if ("no-details".equals(request.getFlowId())) {
                observer.onError(Status.NOT_FOUND
                        .withDescription("plain failure")
                        .asRuntimeException());
                return;
            }
            if ("unknown".equals(request.getFlowId())) {
                observer.onError(error(
                        Status.Code.NOT_FOUND,
                        ErrorResponse.newBuilder()
                                .setSubStatusValue(999)
                                .setDetail("unknown sub-status")
                                .build()));
                return;
            }
            if ("malformed".equals(request.getFlowId())) {
                final Any malformed = Any.newBuilder()
                        .setTypeUrl("type.googleapis.com/dex.ErrorResponse")
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
                        ErrorResponse.newBuilder()
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
                final StreamObserver<WaitForFlowResponse> observer) {
            observer.onError(error(
                    Status.Code.DEADLINE_EXCEEDED,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
                    "long poll timed out"));
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
