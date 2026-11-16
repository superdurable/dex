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

import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class StopCompleteTest {
    private static final String STOP_OUTPUT = "completed by StopFlow";

    @TempDir
    Path cacheDirectory;

    @Test
    void testForceCompleteCancelsActiveStep() throws Exception {
        final StopCompleteWorkflow workflow = new StopCompleteWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = flowId("force-complete");
            environment.client().startFlow(workflow, flowId, 1);
            assertTrue(workflow.executeStarted.await(10, TimeUnit.SECONDS));

            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.FORCE_COMPLETE, null, STOP_OUTPUT));

            final FlowResult result =
                    environment.client().waitForFlow(flowId, Duration.ofSeconds(15));
            workflow.releaseExecute.countDown();
            assertOnlyStopOutput(result);
        }
    }

    @Test
    void testGracefulCompleteWaitsForActiveStep() throws Exception {
        final StopCompleteWorkflow workflow = new StopCompleteWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = flowId("graceful-complete");
            environment.client().startFlow(workflow, flowId, 1);
            assertTrue(workflow.executeStarted.await(10, TimeUnit.SECONDS));

            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.GRACEFUL_COMPLETE, null, STOP_OUTPUT));

            assertEquals(
                    FlowStatus.RUNNING,
                    environment.client().describeFlow(flowId).getStatus());
            workflow.releaseExecute.countDown();
            assertOnlyStopOutput(
                    environment.client().waitForFlow(flowId, Duration.ofSeconds(15)));
        }
    }

    @Test
    void testOutputWithCancelIsRejectedBeforeSending() throws Exception {
        final StopCompleteWorkflow workflow = new StopCompleteWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                workflow)) {
            final String flowId = flowId("cancel-output");
            environment.client().startFlow(workflow, flowId, 1);
            assertTrue(workflow.executeStarted.await(10, TimeUnit.SECONDS));

            final IllegalArgumentException failure = assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().stopFlow(
                            flowId,
                            new StopFlowOptions(StopType.CANCEL, null, STOP_OUTPUT)));
            assertTrue(failure.getMessage().contains("StopFlowOptions output requires"));
            assertEquals(
                    FlowStatus.RUNNING,
                    environment.client().describeFlow(flowId).getStatus());
            workflow.releaseExecute.countDown();
        }
    }

    private static void assertOnlyStopOutput(final FlowResult result) {
        assertEquals(FlowStatus.COMPLETED, result.getStatus());
        assertEquals(1, result.getCompletions().size());
        final StepCompletion completion = result.getCompletions().get(0);
        assertEquals("", completion.getStepType());
        assertEquals("", completion.getStepExecutionId());
        assertEquals(STOP_OUTPUT, completion.getOutput(String.class));
    }

    private static String flowId(final String prefix) {
        return "java-stop-complete-" + prefix + "-" + UUID.randomUUID();
    }
}
