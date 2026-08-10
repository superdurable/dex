/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Super Durable Source License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.superdurable.dex.Flow;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import io.superdurable.gen.ActiveStepExecutionState;
import io.superdurable.gen.FlowHistoryEvent;
import io.superdurable.gen.FlowServiceGrpc;
import io.superdurable.gen.GetFlowStateRequest;
import io.superdurable.gen.GetFlowStateResponse;
import io.superdurable.gen.GetHistoryEventsRequest;
import io.superdurable.gen.StepMethodFailure;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

@Tag("dex-dev")
public final class WorkerFailureTest {
    @TempDir
    Path cacheDirectory;

    @Test
    void testWaitForFailureIsVisibleWhileRetryingAndInHistory() throws Exception {
        verifyRetryFailure(
                new WorkerFailureWaitForWorkflow(),
                true,
                "Java waitFor retry failure");
    }

    @Test
    void testExecuteFailureIsVisibleWhileRetryingAndInHistory() throws Exception {
        verifyRetryFailure(
                new WorkerFailureExecuteWorkflow(),
                false,
                "Java execute retry failure");
    }

    private void verifyRetryFailure(
            final Flow<Void> workflow,
            final boolean waitFor,
            final String expectedDetail) throws Exception {
        final String serverAddress = System.getProperty(
                "dex.test.serverAddress",
                "127.0.0.1:8801");
        final ManagedChannel channel = ManagedChannelBuilder.forTarget(serverAddress)
                .usePlaintext()
                .build();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = "java-worker-failure-" + UUID.randomUUID();
            final String runId = environment.client().startFlow(workflow, flowId, null);
            final FlowServiceGrpc.FlowServiceBlockingStub service =
                    FlowServiceGrpc.newBlockingStub(channel);

            final StepMethodFailure liveFailure = awaitLiveFailure(service, flowId, runId);
            assertWorkerFailure(liveFailure, expectedDetail);

            environment.client().waitForFlow(flowId, Void.class, Duration.ofSeconds(30));
            final StepMethodFailure historyFailure = historyFailure(
                    service,
                    flowId,
                    runId,
                    waitFor);
            assertWorkerFailure(historyFailure, expectedDetail);
        } finally {
            channel.shutdownNow();
        }
    }

    private static StepMethodFailure awaitLiveFailure(
            final FlowServiceGrpc.FlowServiceBlockingStub service,
            final String flowId,
            final String runId) {
        final long deadline = System.nanoTime() + Duration.ofSeconds(4).toNanos();
        while (System.nanoTime() < deadline) {
            final GetFlowStateResponse state = service.getFlowState(
                    GetFlowStateRequest.newBuilder()
                            .setFlowId(flowId)
                            .setRunId(runId)
                            .build());
            for (ActiveStepExecutionState step : state.getActiveStepExecutionsList()) {
                if (step.hasLastFailureInfo()) {
                    return step.getLastFailureInfo();
                }
            }
            Thread.yield();
        }
        return fail("active Step did not expose its retry failure");
    }

    private static StepMethodFailure historyFailure(
            final FlowServiceGrpc.FlowServiceBlockingStub service,
            final String flowId,
            final String runId,
            final boolean waitFor) {
        for (FlowHistoryEvent event : service.getHistoryEvents(
                GetHistoryEventsRequest.newBuilder()
                        .setFlowId(flowId)
                        .setRunId(runId)
                        .build()).getEventsList()) {
            if (waitFor && event.hasStepWaitForCompleted()
                    && event.getStepWaitForCompleted().getContext().hasLastFailureInfo()) {
                return event.getStepWaitForCompleted().getContext().getLastFailureInfo();
            }
            if (!waitFor && event.hasStepExecuteCompleted()
                    && event.getStepExecuteCompleted().getContext().hasLastFailureInfo()) {
                return event.getStepExecuteCompleted().getContext().getLastFailureInfo();
            }
        }
        return fail("completed Step event did not preserve its last failure");
    }

    private static void assertWorkerFailure(
            final StepMethodFailure failure,
            final String expectedDetail) {
        assertNotNull(failure);
        assertEquals(1, failure.getAttempt());
        assertEquals(expectedDetail, failure.getDetails().getDetail());
        assertEquals(
                IllegalStateException.class.getName(),
                failure.getDetails().getOriginalWorkerErrorType());
        assertEquals(
                expectedDetail,
                failure.getDetails().getOriginalWorkerErrorDetail());
        assertEquals(
                Status.Code.INTERNAL.value(),
                failure.getDetails().getOriginalWorkerErrorStatus());
        assertTrue(failure.getDetails().getOriginalWorkerErrorStackTrace()
                .contains(IllegalStateException.class.getName()));
        assertTrue(failure.getDetails().getOriginalWorkerErrorStackTrace()
                .contains(expectedDetail));
    }
}
