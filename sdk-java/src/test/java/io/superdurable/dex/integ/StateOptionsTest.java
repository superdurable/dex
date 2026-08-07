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

import io.superdurable.dex.Client;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;

@Tag("dex-dev")
public final class StateOptionsTest {
    private static final StateOptionsWorkflow WORKFLOW = new StateOptionsWorkflow();
    private static final StateOptionsLockingWorkflow LOCKING_WORKFLOW =
            new StateOptionsLockingWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testStateOptionsWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "state-options-" + UUID.randomUUID();
            environment.client().startFlow(WORKFLOW, flowId, null);
            assertEquals("success", environment.client().waitForFlow(
                    flowId,
                    String.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testWaitForAndExecuteLocksSerializeParallelSteps() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                LOCKING_WORKFLOW)) {
            final String flowId = "state-options-locks-" + UUID.randomUUID();
            final int parallelism = 20;
            environment.client().startFlow(LOCKING_WORKFLOW, flowId, parallelism);
            assertEquals("20:20", environment.client().waitForFlow(
                    flowId,
                    String.class,
                    Duration.ofSeconds(30)));
            assertEquals(
                    parallelism,
                    environment.client().getAttribute(flowId, LOCKING_WORKFLOW.waitForCount));
            assertEquals(
                    parallelism,
                    environment.client().getAttribute(flowId, LOCKING_WORKFLOW.executeCount));
        }
    }

    void compileStepLocks(final Client client) {
        client.startFlow(LOCKING_WORKFLOW, "state-options-locks", 10);
        final String output = client.waitForFlow("state-options-locks", String.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
