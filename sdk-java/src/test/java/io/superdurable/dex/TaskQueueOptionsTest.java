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

import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.Empty;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Server;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import io.grpc.stub.StreamObserver;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.InvokeWaitForMethodRequest;
import io.superdurable.gen.InvokeWaitForMethodResponse;
import io.superdurable.gen.ServerInfo;
import io.superdurable.gen.StartFlowRequest;
import io.superdurable.gen.StartFlowResponse;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class TaskQueueOptionsTest {
    private static final ManagedChannel UNUSED_CHANNEL =
            ManagedChannelBuilder.forTarget("127.0.0.1:1").usePlaintext().build();
    private static final String SIXTY_THREE_CHARACTERS = String.join("", Collections.nCopies(63, "q"));

    @AfterAll
    static void closeUnusedChannel() throws InterruptedException {
        UNUSED_CHANNEL.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
    }

    @Test
    void validatesTaskQueueNamesForWorkersAndFlows() {
        for (String valid : Arrays.asList("", "blue", "a.b_c-D9", SIXTY_THREE_CHARACTERS)) {
            PullWorkerOptions.newBuilder().taskQueue(valid).build();
            StartFlowOptions.newBuilder().taskQueue(valid).build();
            SubFlowOptions.newBuilder().taskQueue(valid).build();
        }
        StartFlowOptions.newBuilder().taskQueue("dex.default").build();
        SubFlowOptions.newBuilder().taskQueue("dex.default").build();
        assertThrows(IllegalArgumentException.class,
                () -> PullWorkerOptions.newBuilder().taskQueue("dex.default"));
        for (String invalid : Arrays.asList(null, SIXTY_THREE_CHARACTERS + "q", "blue queue", "blue/green",
                "blüe", "dex.reserved")) {
            assertThrows(IllegalArgumentException.class,
                    () -> PullWorkerOptions.newBuilder().taskQueue(invalid), String.valueOf(invalid));
            assertThrows(IllegalArgumentException.class,
                    () -> StartFlowOptions.newBuilder().taskQueue(invalid), String.valueOf(invalid));
            assertThrows(IllegalArgumentException.class,
                    () -> SubFlowOptions.newBuilder().taskQueue(invalid), String.valueOf(invalid));
        }
    }

    @Test
    void startFlowSendsTheTaskQueueOnlyToProtocolFiveServers() throws Exception {
        final TaskQueueOptionsFlow flow = new TaskQueueOptionsFlow();
        try (StartFlowService service = new StartFlowService(5);
                Client client = service.newClient(flow)) {
            client.startFlow(flow, "default-queue", "input");
            client.startFlow(flow, "blue-queue", "input",
                    StartFlowOptions.newBuilder().taskQueue("blue").build());
            client.startFlow(flow, "explicit-default-queue", "input",
                    StartFlowOptions.newBuilder().taskQueue("dex.default").build());
            assertEquals(3, service.startRequests.size());
            assertEquals(io.superdurable.gen.WorkerTarget.newBuilder().setIsPullMode(true).build(),
                    workerTarget(service.startRequests.get(0)));
            assertEquals(io.superdurable.gen.WorkerTarget.newBuilder().setIsPullMode(true)
                    .setTaskQueue("blue").build(), workerTarget(service.startRequests.get(1)));
            assertEquals("dex.default", workerTarget(service.startRequests.get(2)).getTaskQueue());
        }
        try (StartFlowService service = new StartFlowService(4);
                Client client = service.newClient(flow)) {
            final IllegalStateException failure = assertThrows(IllegalStateException.class,
                    () -> client.startFlow(flow, "blue-queue", "input",
                            StartFlowOptions.newBuilder().taskQueue("blue").build()));
            assertTrue(failure.getMessage().contains("requires Server protocol version 5"),
                    failure.getMessage());
            assertTrue(service.startRequests.isEmpty());
            client.startFlow(flow, "default-queue", "input");
            assertEquals(1, service.startRequests.size());
        }
    }

    @Test
    void subFlowOverrideCarriesTheTaskQueueOnlyOnProtocolFive() throws Throwable {
        final Registry registry = new Registry(Arrays.<Flow<?>>asList(
                new TaskQueueOptionsParentFlow(), new TaskQueueOptionsFlow()));
        final ValueMapper values = new ValueMapper(new ObjectMapper());
        final NegotiatedServerProtocol protocolFive = new NegotiatedServerProtocol();
        protocolFive.recordNegotiatedVersion(5);
        final WorkerDispatcher dispatcher = new WorkerDispatcher(registry, values,
                new ValueHydrator(FlowServiceGrpc.newBlockingStub(UNUSED_CHANNEL), new EmptyBlobCache()),
                protocolFive);
        for (String taskQueue : Arrays.asList("", "reports", "dex.default")) {
            final io.superdurable.gen.WorkerTarget target = subFlowWorkerTarget(
                    dispatcher.invokeWaitFor(subFlowRequest(values, taskQueue), null));
            assertTrue(target.getIsPullMode());
            assertEquals(taskQueue, target.getTaskQueue());
        }
        final NegotiatedServerProtocol protocolFour = new NegotiatedServerProtocol();
        protocolFour.recordNegotiatedVersion(4);
        for (NegotiatedServerProtocol olderProtocol
                : Arrays.asList(protocolFour, new NegotiatedServerProtocol())) {
            final WorkerDispatcher olderDispatcher = new WorkerDispatcher(registry, values,
                    new ValueHydrator(FlowServiceGrpc.newBlockingStub(UNUSED_CHANNEL), new EmptyBlobCache()),
                    olderProtocol);
            final IllegalStateException failure = assertThrows(IllegalStateException.class,
                    () -> olderDispatcher.invokeWaitFor(subFlowRequest(values, "reports"), null));
            assertTrue(failure.getMessage().contains("requires Server protocol version 5"),
                    failure.getMessage());
            assertEquals("", subFlowWorkerTarget(olderDispatcher.invokeWaitFor(
                    subFlowRequest(values, ""), null)).getTaskQueue());
        }
    }

    private static io.superdurable.gen.WorkerTarget workerTarget(final StartFlowRequest request) {
        return request.getFlowStartOptions().getFlowConfigOverride().getWorkerTarget();
    }

    private static io.superdurable.gen.WorkerTarget subFlowWorkerTarget(
            final InvokeWaitForMethodResponse response) {
        return response.getWaitingCondition().getSubFlowConditions(0).getOptions()
                .getFlowConfigOverride().getWorkerTarget();
    }

    private static InvokeWaitForMethodRequest subFlowRequest(final ValueMapper values, final String taskQueue) {
        return InvokeWaitForMethodRequest.newBuilder()
                .setFlowType("TaskQueueOptionsParentFlow")
                .setStepType("TaskQueueOptionsParentStep")
                .setContext(io.superdurable.gen.Context.newBuilder().setFlowId("parent"))
                .setStepInput(values.encode(taskQueue))
                .build();
    }

    private static final class TaskQueueOptionsParentFlow implements Flow<String> {
        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(new TaskQueueOptionsParentStep());
        }
    }

    private static final class TaskQueueOptionsParentStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String taskQueue) {
            return Wait.until(SubFlow.run(TaskQueueOptionsFlow.class, "child",
                    SubFlowOptions.newBuilder().taskQueue(taskQueue).build()));
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class TaskQueueOptionsFlow implements Flow<String> {
        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(new TaskQueueOptionsStep());
        }
    }

    private static final class TaskQueueOptionsStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.gracefulComplete(input);
        }
    }

    private static final class StartFlowService extends FlowServiceGrpc.FlowServiceImplBase
            implements AutoCloseable {
        private final int currentProtocolVersion;
        private final Server server;
        private final List<StartFlowRequest> startRequests = new CopyOnWriteArrayList<StartFlowRequest>();

        private StartFlowService(final int currentProtocolVersion) throws IOException {
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
        public void startFlow(final StartFlowRequest request, final StreamObserver<StartFlowResponse> observer) {
            startRequests.add(request);
            observer.onNext(StartFlowResponse.newBuilder().setRunId("run-" + startRequests.size()).build());
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
