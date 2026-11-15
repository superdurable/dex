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

import com.google.protobuf.Any;
import com.google.protobuf.Empty;
import io.grpc.Server;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.protobuf.StatusProto;
import io.grpc.stub.StreamObserver;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.gen.CloneForkFlowRequest;
import io.superdurable.gen.CloneForkFlowResponse;
import io.superdurable.gen.FlowCloneForkPointType;
import io.superdurable.gen.FlowResetStepMethod;
import io.superdurable.gen.FlowResetType;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.ResetFlowRequest;
import io.superdurable.gen.ResetFlowResponse;
import io.superdurable.gen.ServerInfo;
import io.superdurable.gen.ServiceErrorResponse;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.time.Duration;
import java.time.Instant;
import java.util.Collections;
import java.util.List;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class CloneForkFlowTest {
    private static final Attribute<String> LABEL = Attribute.define("clone-fork-label", String.class);

    @Test
    void defaultOptionsCopyTheCurrentPointAndGenerateARequestId() throws Exception {
        final CloneForkFlowTestFlow flow = new CloneForkFlowTestFlow();
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(flow)) {
            final CloneForkFlowResult result = client.cloneForkFlow(
                    "source-1", "clone-1", CloneForkFlowOptions.newBuilder().build());
            assertEquals("clone-run-1", result.getRunId());
            assertEquals("source-run-1", result.getSourceRunId());
            assertEquals(2, result.getSourceRunAttempt());
            assertEquals(17L, result.getSourceEventId());

            final CloneForkFlowRequest request = service.cloneRequests.get(0);
            assertEquals("source-1", request.getSourceFlowId());
            assertEquals("clone-1", request.getFlowId());
            assertEquals("", request.getSourceRunId());
            UUID.fromString(request.getRequestId());
            assertEquals(FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_CURRENT, request.getPointType());
            assertEquals(FlowResetStepMethod.FLOW_RESET_STEP_METHOD_UNSPECIFIED, request.getStepMethod());
            assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED,
                    request.getSubFlowResetPolicy());
            assertEquals(io.superdurable.gen.IdReusePolicy.ID_REUSE_POLICY_UNSPECIFIED,
                    request.getIdReusePolicy());
            assertFalse(request.hasFlowConfigOverride(), "no override keeps the source FlowConfig");
            assertFalse(request.hasFlowAlreadyStartedOptions());
            assertEquals(0, request.getAttributesCount());
            assertEquals(0, request.getFlowTimeoutSeconds());
            assertEquals(0, request.getRunAttempt());
            assertEquals(0L, request.getEventId());

            client.cloneForkFlow("source-1", "clone-2", CloneForkFlowOptions.newBuilder().build());
            assertNotEquals(request.getRequestId(), service.cloneRequests.get(1).getRequestId());
        }
    }

    @Test
    void mapsEveryOption() throws Exception {
        final CloneForkFlowTestFlow flow = new CloneForkFlowTestFlow();
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(flow)) {
            client.cloneForkFlow("source-1", "clone-1", CloneForkFlowOptions.newBuilder()
                    .sourceRunId("source-run-9")
                    .sourceRunAttempt(3)
                    .pointType(TimeTravelType.EVENT_ID)
                    .eventId(42)
                    .idReusePolicy(IdReusePolicy.TERMINATE_IF_RUNNING)
                    .ignoreAlreadyStarted(true)
                    .requestId("branch-request")
                    .configOverride(FlowConfig.newBuilder().historyWindowEvents(64).build())
                    .taskQueue("tenant-b")
                    .addAttribute(LABEL, "copied")
                    .timeout(Duration.ofSeconds(90))
                    .reason("branch the session")
                    .subFlowResetPolicy(SubFlowResetPolicy.ATTACH_ELSE_FAIL)
                    .build());
            final CloneForkFlowRequest request = service.cloneRequests.get(0);
            assertEquals("source-run-9", request.getSourceRunId());
            assertEquals(3, request.getRunAttempt());
            assertEquals(FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID, request.getPointType());
            assertEquals(42L, request.getEventId());
            assertEquals(io.superdurable.gen.IdReusePolicy.ID_REUSE_POLICY_ALLOW_TERMINATE_IF_RUNNING,
                    request.getIdReusePolicy());
            assertTrue(request.getFlowAlreadyStartedOptions().getIgnoreAlreadyStartedError());
            assertEquals("branch-request", request.getRequestId());
            assertEquals(64, request.getFlowConfigOverride().getHistoryWindowEvents());
            assertFalse(request.getFlowConfigOverride().hasStepDurability());
            assertEquals("tenant-b", request.getFlowConfigOverride().getWorkerTarget().getTaskQueue());
            assertTrue(request.getFlowConfigOverride().getWorkerTarget().getIsPullMode());
            assertEquals(1, request.getAttributesCount());
            assertEquals("clone-fork-label", request.getAttributes(0).getKey());
            assertEquals(90, request.getFlowTimeoutSeconds());
            assertEquals("branch the session", request.getReason());
            assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL,
                    request.getSubFlowResetPolicy());
        }
    }

    @Test
    void mapsEveryPointType() {
        final Client client = unconnectedClient();
        assertPoint(client, CloneForkFlowOptions.newBuilder().pointType(TimeTravelType.CURRENT),
                FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_CURRENT);
        assertPoint(client, CloneForkFlowOptions.newBuilder().pointType(TimeTravelType.BEGINNING)
                        .sourceRunAttempt(1),
                FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_BEGINNING);
        final CloneForkFlowRequest timeRequest = assertPoint(client, CloneForkFlowOptions.newBuilder()
                        .pointType(TimeTravelType.HISTORY_EVENT_TIME)
                        .historyEventTime(Instant.parse("2026-11-15T10:30:00Z")),
                FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_HISTORY_EVENT_TIME);
        assertEquals("2026-11-15T10:30:00Z", timeRequest.getHistoryEventTime());
        assertEquals("ChargeOrder", assertPoint(client, CloneForkFlowOptions.newBuilder()
                        .pointType(TimeTravelType.STEP_TYPE).stepType("ChargeOrder"),
                FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_STEP_TYPE).getStepType());
        final CloneForkFlowRequest executionRequest = assertPoint(client, CloneForkFlowOptions.newBuilder()
                        .pointType(TimeTravelType.STEP_EXECUTION_ID)
                        .stepExecutionId("ChargeOrder-2")
                        .stepMethod(TimeTravelStepMethod.WAIT_FOR),
                FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_STEP_EXECUTION_ID);
        assertEquals("ChargeOrder-2", executionRequest.getStepExecutionId());
        assertEquals(FlowResetStepMethod.FLOW_RESET_STEP_METHOD_WAIT_FOR, executionRequest.getStepMethod());
        assertEquals(7L, assertPoint(client, CloneForkFlowOptions.newBuilder()
                        .pointType(TimeTravelType.EVENT_ID).eventId(7),
                FlowCloneForkPointType.FLOW_CLONE_FORK_POINT_TYPE_EVENT_ID).getEventId());
    }

    @Test
    void rejectsInvalidPointsAndOptions() {
        final Client client = unconnectedClient();
        assertRejected(client, CloneForkFlowOptions.newBuilder().eventId(3));
        assertRejected(client, CloneForkFlowOptions.newBuilder().sourceRunAttempt(1));
        assertRejected(client, CloneForkFlowOptions.newBuilder()
                .pointType(TimeTravelType.BEGINNING).sourceRunAttempt(-1));
        assertRejected(client, CloneForkFlowOptions.newBuilder().pointType(TimeTravelType.EVENT_ID));
        assertRejected(client, CloneForkFlowOptions.newBuilder().pointType(TimeTravelType.EVENT_ID).eventId(-4));
        assertRejected(client, CloneForkFlowOptions.newBuilder()
                .pointType(TimeTravelType.EVENT_ID).eventId(4).stepType("A"));
        assertRejected(client, CloneForkFlowOptions.newBuilder()
                .pointType(TimeTravelType.STEP_TYPE).stepType("A").stepMethod(TimeTravelStepMethod.EXECUTE));
        assertRejected(client, CloneForkFlowOptions.newBuilder()
                .pointType(TimeTravelType.STEP_EXECUTION_ID).stepExecutionId("A-1"));
        assertThrows(IllegalArgumentException.class,
                () -> CloneForkFlowOptions.newBuilder().timeout(Duration.ofSeconds(-1)));
        assertThrows(IllegalArgumentException.class,
                () -> CloneForkFlowOptions.newBuilder().requestId(""));
        assertThrows(IllegalArgumentException.class,
                () -> CloneForkFlowOptions.newBuilder().taskQueue("dex.other"));
        assertThrows(IllegalArgumentException.class,
                () -> client.cloneForkFlow("same", "same", CloneForkFlowOptions.newBuilder().build()));
        assertThrows(IllegalArgumentException.class,
                () -> client.cloneForkFlow("", "clone-1", CloneForkFlowOptions.newBuilder().build()));
    }

    @Test
    void refusesServersOlderThanProtocolSix() throws Exception {
        final CloneForkFlowTestFlow flow = new CloneForkFlowTestFlow();
        try (CloneForkService service = new CloneForkService(5);
                Client client = service.newClient(flow)) {
            final IllegalStateException failure = assertThrows(IllegalStateException.class,
                    () -> client.cloneForkFlow("source-1", "clone-1", CloneForkFlowOptions.newBuilder().build()));
            assertTrue(failure.getMessage().contains("requires Server protocol version 6"), failure.getMessage());
            assertTrue(failure.getMessage().contains("server-zd 0.1.33"), failure.getMessage());
            assertTrue(service.cloneRequests.isEmpty());
        }
    }

    @Test
    void translatesNotFoundAndAlreadyStarted() throws Exception {
        final CloneForkFlowTestFlow flow = new CloneForkFlowTestFlow();
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(flow)) {
            service.failure = error(Status.Code.NOT_FOUND,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_FLOW_NOT_EXISTS, "source missing");
            assertThrows(FlowNotFoundException.class,
                    () -> client.cloneForkFlow("source-1", "clone-1", CloneForkFlowOptions.newBuilder().build()));
            service.failure = error(Status.Code.ALREADY_EXISTS,
                    io.superdurable.gen.ErrorSubStatus.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED, "already started");
            assertThrows(FlowAlreadyStartedException.class,
                    () -> client.cloneForkFlow("source-1", "clone-1", CloneForkFlowOptions.newBuilder().build()));
        }
    }

    @Test
    void timeTravelSendsEventIdAndSubFlowResetPolicy() throws Exception {
        final CloneForkFlowTestFlow flow = new CloneForkFlowTestFlow();
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(flow)) {
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID)
                    .eventId(12)
                    .subFlowResetPolicy(SubFlowResetPolicy.ALWAYS_REJECT)
                    .build());
            final ResetFlowRequest request = service.resetRequests.get(0);
            assertEquals(FlowResetType.FLOW_RESET_TYPE_EVENT_ID, request.getResetType());
            assertEquals(12L, request.getEventId());
            assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT,
                    request.getSubFlowResetPolicy());
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING).build());
            assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED,
                    service.resetRequests.get(1).getSubFlowResetPolicy());
            assertEquals(0L, service.resetRequests.get(1).getEventId());
            assertThrows(IllegalArgumentException.class, () -> client.timeTravel("flow-1",
                    TimeTravelOptions.newBuilder(TimeTravelType.CURRENT).build()));
            assertThrows(IllegalArgumentException.class, () -> client.timeTravel("flow-1",
                    TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID).build()));
            assertEquals(2, service.resetRequests.size());
        }
    }

    @Test
    void timeTravelMapsEveryPointWithoutAnEventIdForOtherTypes() throws Exception {
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(new CloneForkFlowTestFlow())) {
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING).build());
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.HISTORY_EVENT_TIME)
                    .historyEventTime(Instant.parse("2026-01-01T00:00:00Z")).build());
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.STEP_TYPE)
                    .stepType("ChargeOrder").build());
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.STEP_EXECUTION_ID)
                    .stepExecutionId("step-1").stepMethod(TimeTravelStepMethod.WAIT_FOR).build());
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID)
                    .eventId(7).build());

            final FlowResetType[] types = {
                FlowResetType.FLOW_RESET_TYPE_BEGINNING,
                FlowResetType.FLOW_RESET_TYPE_HISTORY_EVENT_TIME,
                FlowResetType.FLOW_RESET_TYPE_STEP_TYPE,
                FlowResetType.FLOW_RESET_TYPE_STEP_EXECUTION_ID,
                FlowResetType.FLOW_RESET_TYPE_EVENT_ID,
            };
            assertEquals(types.length, service.resetRequests.size());
            for (int index = 0; index < types.length; index++) {
                assertEquals(types[index], service.resetRequests.get(index).getResetType());
                assertEquals(index == 4 ? 7L : 0L, service.resetRequests.get(index).getEventId());
            }
            assertEquals("2026-01-01T00:00:00Z", service.resetRequests.get(1).getHistoryEventTime());
            assertEquals("ChargeOrder", service.resetRequests.get(2).getStepType());
            assertEquals("step-1", service.resetRequests.get(3).getStepExecutionId());
            assertEquals(FlowResetStepMethod.FLOW_RESET_STEP_METHOD_WAIT_FOR,
                    service.resetRequests.get(3).getStepMethod());
        }
    }

    @Test
    void timeTravelRejectsMixedPointSelectorsBeforeSending() throws Exception {
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(new CloneForkFlowTestFlow())) {
            final TimeTravelOptions.Builder[] invalid = {
                TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING).eventId(3),
                TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING).eventId(0),
                TimeTravelOptions.newBuilder(TimeTravelType.HISTORY_EVENT_TIME)
                        .historyEventTime(Instant.parse("2026-01-01T00:00:00Z")).eventId(3),
                TimeTravelOptions.newBuilder(TimeTravelType.STEP_TYPE).stepType("A").eventId(3),
                TimeTravelOptions.newBuilder(TimeTravelType.STEP_EXECUTION_ID)
                        .stepExecutionId("A-1").stepMethod(TimeTravelStepMethod.EXECUTE).eventId(3),
                TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID).eventId(3)
                        .historyEventTime(Instant.parse("2026-01-01T00:00:00Z")),
                TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID).eventId(3).stepType("A"),
                TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID).eventId(3).stepExecutionId("A-1"),
                TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID).eventId(3)
                        .stepMethod(TimeTravelStepMethod.EXECUTE),
            };
            for (TimeTravelOptions.Builder options : invalid) {
                assertThrows(IllegalArgumentException.class,
                        () -> client.timeTravel("flow-1", options.build()));
            }
            assertEquals(0, service.resetRequests.size());
        }
    }

    @Test
    void timeTravelPreservesEarlierRequestsWithUnusedSelectors() throws Exception {
        try (CloneForkService service = new CloneForkService(6);
                Client client = service.newClient(new CloneForkFlowTestFlow())) {
            client.timeTravel("flow-1", TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING)
                    .stepType("unused").build());
            final ResetFlowRequest request = service.resetRequests.get(0);
            assertEquals(FlowResetType.FLOW_RESET_TYPE_BEGINNING, request.getResetType());
            assertEquals("unused", request.getStepType());
            assertEquals(0L, request.getEventId());
        }
    }

    @Test
    void mapsEverySubFlowResetPolicy() {
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_UNSPECIFIED,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.DEFAULT));
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_RESTART,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.ALWAYS_RESTART));
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_FAIL,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.ALWAYS_FAIL));
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ALWAYS_REJECT,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.ALWAYS_REJECT));
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_RESTART,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.ATTACH_ELSE_RESTART));
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_FAIL,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.ATTACH_ELSE_FAIL));
        assertEquals(io.superdurable.gen.SubFlowResetPolicy.SUB_FLOW_RESET_POLICY_ATTACH_ELSE_REJECT,
                Client.mapSubFlowResetPolicy(SubFlowResetPolicy.ATTACH_ELSE_REJECT));
    }

    private static CloneForkFlowRequest assertPoint(
            final Client client,
            final CloneForkFlowOptions.Builder options,
            final FlowCloneForkPointType expected) {
        final CloneForkFlowRequest request = client.mapCloneForkFlowOptions(options.build()).build();
        assertEquals(expected, request.getPointType());
        return request;
    }

    private static void assertRejected(final Client client, final CloneForkFlowOptions.Builder options) {
        final CloneForkFlowOptions built = options.build();
        assertThrows(IllegalArgumentException.class, () -> client.mapCloneForkFlowOptions(built));
    }

    private static Client unconnectedClient() {
        return new Client(new Registry(Collections.<Flow<?>>singletonList(new CloneForkFlowTestFlow())),
                new EmptyBlobCache(), new ClientOptions("127.0.0.1:1"));
    }

    private static StatusRuntimeException error(
            final Status.Code code,
            final io.superdurable.gen.ErrorSubStatus subStatus,
            final String detail) {
        final ServiceErrorResponse response = ServiceErrorResponse.newBuilder()
                .setSubStatus(subStatus).setDetail(detail).build();
        return StatusProto.toStatusRuntimeException(com.google.rpc.Status.newBuilder()
                .setCode(code.value())
                .setMessage(detail)
                .addDetails(Any.pack(response))
                .build());
    }

    private static final class CloneForkFlowTestFlow implements Flow<String> {
        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(new CloneForkFlowTestStep());
        }

        @Override
        public PersistenceSchema getPersistenceSchema() {
            return PersistenceSchema.of(LABEL);
        }
    }

    private static final class CloneForkFlowTestStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class CloneForkService extends FlowServiceGrpc.FlowServiceImplBase
            implements AutoCloseable {
        private final int currentProtocolVersion;
        private final Server server;
        private final List<CloneForkFlowRequest> cloneRequests = new CopyOnWriteArrayList<CloneForkFlowRequest>();
        private final List<ResetFlowRequest> resetRequests = new CopyOnWriteArrayList<ResetFlowRequest>();
        private volatile StatusRuntimeException failure;

        private CloneForkService(final int currentProtocolVersion) throws IOException {
            this.currentProtocolVersion = currentProtocolVersion;
            this.server = NettyServerBuilder.forAddress(new java.net.InetSocketAddress("127.0.0.1", 0))
                    .addService(this).build().start();
        }

        private Client newClient(final Flow<?> flow) {
            return new Client(new Registry(Collections.<Flow<?>>singletonList(flow)), new EmptyBlobCache(),
                    new ClientOptions("127.0.0.1:" + server.getPort()));
        }

        @Override
        public void getServerInfo(final Empty request, final StreamObserver<ServerInfo> observer) {
            observer.onNext(ServerInfo.newBuilder().setServerVersion("test")
                    .setMinimumSupportedProtocolVersion(1)
                    .setCurrentProtocolVersion(currentProtocolVersion).build());
            observer.onCompleted();
        }

        @Override
        public void cloneForkFlow(
                final CloneForkFlowRequest request,
                final StreamObserver<CloneForkFlowResponse> observer) {
            cloneRequests.add(request);
            if (failure != null) {
                observer.onError(failure);
                return;
            }
            observer.onNext(CloneForkFlowResponse.newBuilder()
                    .setRunId("clone-run-1")
                    .setSourceRunId("source-run-1")
                    .setSourceRunAttempt(2)
                    .setSourceEventId(17)
                    .build());
            observer.onCompleted();
        }

        @Override
        public void resetFlow(final ResetFlowRequest request, final StreamObserver<ResetFlowResponse> observer) {
            resetRequests.add(request);
            observer.onNext(ResetFlowResponse.newBuilder().setRunId("run-1").setRunAttempt(2).build());
            observer.onCompleted();
        }

        @Override
        public void close() throws InterruptedException {
            server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    private static final class EmptyBlobCache implements BlobCache {
        @Override
        public Optional<byte[]> get(final String blobID) {
            return Optional.empty();
        }

        @Override
        public boolean put(final String blobID, final byte[] payload) {
            return false;
        }

        @Override
        public void delete(final String blobID) {
        }

        @Override
        public void deleteAll() {
        }

        @Override
        public void close() {
        }
    }
}
