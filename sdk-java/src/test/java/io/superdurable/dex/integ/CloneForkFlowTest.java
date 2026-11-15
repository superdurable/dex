/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.integ;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.superdurable.dex.CloneForkFlowOptions;
import io.superdurable.dex.CloneForkFlowResult;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.SubFlowResetPolicy;
import io.superdurable.dex.TimeTravelOptions;
import io.superdurable.dex.TimeTravelType;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import io.superdurable.gen.FlowHistoryEvent;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetHistoryEventsRequest;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

@Tag("dex-dev")
public final class CloneForkFlowTest {
    private static final CloneForkSessionWorkflow WORKFLOW = new CloneForkSessionWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void clonesARunningSourceAtTheCurrentAndEventIdPoints() throws Exception {
        final ManagedChannel channel = ManagedChannelBuilder
                .forTarget(DexDevTestEnvironment.serverAddress()).usePlaintext().build();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(cacheDirectory, WORKFLOW)) {
            final FlowServiceGrpc.FlowServiceBlockingStub service = FlowServiceGrpc.newBlockingStub(channel);
            final String sourceId = "clone-fork-source-" + UUID.randomUUID();
            final String sourceRunId = environment.client().startFlow(WORKFLOW, sourceId, "source");
            final long waitEventId = awaitWaitForCompletedEventId(service, sourceId, sourceRunId);

            final String currentCloneId = "clone-fork-current-" + UUID.randomUUID();
            final CloneForkFlowResult currentClone = environment.client().cloneForkFlow(
                    sourceId,
                    currentCloneId,
                    CloneForkFlowOptions.newBuilder()
                            .addAttribute(CloneForkSessionWorkflow.LABEL, "current")
                            .reason("branch at the current point")
                            .build());
            assertFalse(currentClone.getRunId().isEmpty());
            assertEquals(sourceRunId, currentClone.getSourceRunId());
            assertEquals(1, currentClone.getSourceRunAttempt());
            assertTrue(currentClone.getSourceEventId() >= waitEventId);

            final String eventCloneId = "clone-fork-event-" + UUID.randomUUID();
            final CloneForkFlowResult eventClone = environment.client().cloneForkFlow(
                    sourceId,
                    eventCloneId,
                    CloneForkFlowOptions.newBuilder()
                            .sourceRunId(sourceRunId)
                            .pointType(TimeTravelType.EVENT_ID)
                            .eventId(waitEventId)
                            .build());
            assertEquals(sourceRunId, eventClone.getSourceRunId());
            assertEquals(waitEventId, eventClone.getSourceEventId());

            release(environment, currentCloneId, "a");
            release(environment, eventCloneId, "b");
            assertEquals("current|a", singleOutput(environment, currentCloneId));
            assertEquals("source|b", singleOutput(environment, eventCloneId));

            final FlowInfo source = environment.client().describeFlow(sourceId);
            assertEquals(FlowStatus.RUNNING, source.getStatus());
            assertEquals(sourceRunId, source.getRunId());
            environment.client().stopFlow(sourceId, new StopFlowOptions(StopType.TERMINATE, "test done"));
        } finally {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void timeTravelByEventIdReturnsToTheWaitingPoint() throws Exception {
        final ManagedChannel channel = ManagedChannelBuilder
                .forTarget(DexDevTestEnvironment.serverAddress()).usePlaintext().build();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(cacheDirectory, WORKFLOW)) {
            final FlowServiceGrpc.FlowServiceBlockingStub service = FlowServiceGrpc.newBlockingStub(channel);
            final String flowId = "time-travel-event-id-" + UUID.randomUUID();
            final String runId = environment.client().startFlow(WORKFLOW, flowId, "first");
            final long waitEventId = awaitWaitForCompletedEventId(service, flowId, runId);
            release(environment, flowId, "a");
            assertEquals("first|a", singleOutput(environment, flowId));

            environment.client().timeTravel(flowId, TimeTravelOptions.newBuilder(TimeTravelType.EVENT_ID)
                    .eventId(waitEventId)
                    .skipWritesReapply(true)
                    .subFlowResetPolicy(SubFlowResetPolicy.ATTACH_ELSE_REJECT)
                    .reason("answer again")
                    .build());
            release(environment, flowId, "b");
            assertEquals("first|b", singleOutput(environment, flowId));
        } finally {
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        }
    }

    private static void release(
            final DexDevTestEnvironment environment,
            final String flowId,
            final String answer) {
        final CloneForkSessionWorkflow stub =
                environment.client().newRpcStub(CloneForkSessionWorkflow.class, flowId);
        environment.client().invokeRPC(stub::releaseSession, answer);
    }

    private static String singleOutput(final DexDevTestEnvironment environment, final String flowId) {
        final FlowResult result = environment.client().waitForFlow(flowId, Duration.ofSeconds(30));
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(1, result.getCompletions().size());
        return result.getCompletions().get(0).getOutput(String.class);
    }

    private static long awaitWaitForCompletedEventId(
            final FlowServiceGrpc.FlowServiceBlockingStub service,
            final String flowId,
            final String runId) throws InterruptedException {
        final long deadline = System.nanoTime() + Duration.ofSeconds(30).toNanos();
        while (System.nanoTime() < deadline) {
            for (FlowHistoryEvent event : service.getHistoryEvents(GetHistoryEventsRequest.newBuilder()
                    .setFlowId(flowId)
                    .setRunId(runId)
                    .setEstimatePageSize(100)
                    .build()).getEventsList()) {
                if (event.hasStepWaitForCompleted()) {
                    return event.getEventId();
                }
            }
            Thread.sleep(20);
        }
        return fail("the source run did not complete its WaitFor");
    }
}
