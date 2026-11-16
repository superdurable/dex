/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import io.superdurable.gen.ActiveStepExecutionState;
import io.superdurable.gen.ActiveStepPhase;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetFlowStateRequest;
import io.superdurable.gen.GetFlowStateResponse;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.Collections;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

@Tag("dex-dev")
public final class GracefulCompletionConditionTest {
    @TempDir
    Path cacheDirectory;

    @Test
    void testGracefulStopWakesAStepWaitingOnTheCondition() throws Exception {
        final GracefulCompletionConditionWorkflow workflow = new GracefulCompletionConditionWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = flowId("wake");
            environment.client().startFlow(workflow, flowId, false);
            awaitGracefulCompletionConditionWaiting(flowId);

            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.GRACEFUL_COMPLETE, null, null));

            assertEquals(
                    FlowStatus.COMPLETED,
                    environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getStatus());
            assertEquals(1, workflow.observations.size());
            assertEquals(Collections.emptyList(), workflow.observations.get(0).consumedMessages);
            assertTrue(workflow.observations.get(0).isGracefulCompleted);
            assertTrue(!workflow.supportObservations.isEmpty());
            assertTrue(workflow.supportObservations.stream().allMatch(Boolean::booleanValue));
        }
    }

    @Test
    void testMessagePublishedBeforeTheStopIsConsumedFirst() throws Exception {
        final GracefulCompletionConditionWorkflow workflow = new GracefulCompletionConditionWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = flowId("message-first");
            environment.client().startFlow(workflow, flowId, true);
            final GracefulCompletionConditionWorkflow stub = environment.client().newRpcStub(
                    GracefulCompletionConditionWorkflow.class,
                    flowId);
            environment.client().invokeRPC(stub::publishMessage, "before-stop");
            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.GRACEFUL_COMPLETE, null, null));
            assertEquals(
                    FlowStatus.RUNNING,
                    environment.client().describeFlow(flowId).getStatus());

            environment.client().invokeRPC(stub::openGate);

            assertEquals(
                    FlowStatus.COMPLETED,
                    environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getStatus());
            assertEquals(2, workflow.observations.size());
            assertEquals(
                    Collections.singletonList("before-stop"),
                    workflow.observations.get(0).consumedMessages);
            assertTrue(workflow.observations.get(0).isGracefulCompleted);
            assertEquals(Collections.emptyList(), workflow.observations.get(1).consumedMessages);
            assertTrue(workflow.observations.get(1).isGracefulCompleted);
        }
    }

    private static void awaitGracefulCompletionConditionWaiting(final String flowId) throws InterruptedException {
        final ManagedChannel channel = ManagedChannelBuilder
                .forTarget(DexDevTestEnvironment.serverAddress())
                .usePlaintext()
                .build();
        try {
            final FlowServiceGrpc.FlowServiceBlockingStub service = FlowServiceGrpc.newBlockingStub(channel);
            final long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(10);
            while (System.nanoTime() < deadline) {
                final GetFlowStateResponse state = service.getFlowState(
                        GetFlowStateRequest.newBuilder().setFlowId(flowId).build());
                for (ActiveStepExecutionState step : state.getActiveStepExecutionsList()) {
                    if (step.getPhase() == ActiveStepPhase.ACTIVE_STEP_PHASE_WAITING
                            && step.getWaitingCondition().getGracefulCompletionConditionsCount() == 1) {
                        return;
                    }
                }
                Thread.sleep(50);
            }
            fail("no Step waits on the graceful completion condition");
        } finally {
            channel.shutdownNow();
            assertTrue(channel.awaitTermination(5, TimeUnit.SECONDS));
        }
    }

    private static String flowId(final String prefix) {
        return "java-graceful-condition-" + prefix + "-" + UUID.randomUUID();
    }
}
