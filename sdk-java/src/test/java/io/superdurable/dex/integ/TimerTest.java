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
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class TimerTest {
    private static final TimerWorkflow WORKFLOW = new TimerWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testBasicTimerWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "basic-timer-" + UUID.randomUUID();
            final long startedAt = System.nanoTime();
            environment.client().startFlow(WORKFLOW, flowId, 5);
            environment.client().waitForStepCompletion(
                    flowId,
                    StepExecutionId.of("TimerStep"),
                    Duration.ofSeconds(10));
            environment.client().waitForFlow(flowId);
            final long elapsedMillis = Duration.ofNanos(
                    System.nanoTime() - startedAt).toMillis();
            assertTrue(
                    elapsedMillis >= 4_000L && elapsedMillis <= 7_000L,
                    "actual duration: " + elapsedMillis);
        }
    }

    void compileTimerAndStepWait(final Client client) {
        client.startFlow(WORKFLOW, "timer", 1);
        client.waitForStepCompletion(
                "timer",
                StepExecutionId.of("TimerStep"),
                Duration.ofSeconds(10));
        client.waitForFlow("timer");
    }
}
