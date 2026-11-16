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

import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.Collections;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

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

            environment.client().stopFlow(
                    flowId,
                    new StopFlowOptions(StopType.GRACEFUL_COMPLETE, null, null));

            assertEquals(
                    FlowStatus.COMPLETED,
                    environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getStatus());
            assertEquals(1, workflow.observations.size());
            assertEquals(Collections.emptyList(), workflow.observations.get(0).consumedMessages);
            assertTrue(workflow.observations.get(0).isGracefulCompleted);
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

    private static String flowId(final String prefix) {
        return "java-graceful-condition-" + prefix + "-" + UUID.randomUUID();
    }
}
