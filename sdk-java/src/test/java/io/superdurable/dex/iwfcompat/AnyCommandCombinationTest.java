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

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Client;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.FlowErrorType;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.FlowUncompletedException;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class AnyCommandCombinationTest {
    private static final AnyCommandCombinationWorkflow WORKFLOW =
            new AnyCommandCombinationWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testStateApiFailWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "any-combination-fail-" + UUID.randomUUID();
            final String runId = environment.client().startFlow(WORKFLOW, flowId, 5);
            final FlowUncompletedException failure = assertThrows(
                    FlowUncompletedException.class,
                    () -> environment.client().waitForFlow(
                            flowId,
                            Integer.class,
                            Duration.ofSeconds(30)));
            assertEquals(runId, failure.getRunId());
            assertEquals(FlowStatus.FAILED, failure.getStatus());
            assertEquals(FlowErrorType.WORKER_API_FAILED, failure.getErrorType());
            assertTrue(failure.getMessage().contains("unknown condition ID"));
            final FlowInfo info = environment.client().describeFlow(flowId);
            assertEquals(runId, info.getRunId());
            assertEquals(FlowStatus.FAILED, info.getStatus());
        }
    }

    void compileStateApiFailure(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(10))
                .build();
        client.startFlow(WORKFLOW, "any-combination", 0, options);
        final Integer result = client.waitForFlow("any-combination", Integer.class);
        consume(result);
    }

    private static void consume(final Object value) {
    }
}
