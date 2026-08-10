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
import io.superdurable.dex.TimerId;
import io.superdurable.dex.exceptions.FlowNotActiveException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

@Tag("dex-dev")
public final class SignalTest {
    private static final SignalWorkflow WORKFLOW = new SignalWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testBasicSignalWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "basic-signal-" + UUID.randomUUID();
            environment.client().startFlow(WORKFLOW, flowId, 1);
            environment.client().publish(flowId, WORKFLOW.first, 2, 3, 5);
            environment.client().publish(flowId, WORKFLOW.third, (Void) null);
            environment.client().publish(flowId, WORKFLOW.signalMap, "one", 4);
            environment.client().skipTimer(
                    flowId,
                    new StepExecutionId("SignalCombinationStep"),
                    TimerId.byConditionId("test-timer-id"));
            assertEquals(6, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
            assertThrows(
                    FlowNotActiveException.class,
                    () -> environment.client().publish(flowId, WORKFLOW.first, 8));
        }
    }

    void compileSignalsAndTimerSkip(final Client client) {
        client.startFlow(WORKFLOW, "signal", 0);
        client.publish("signal", WORKFLOW.first, 1);
        client.publish("signal", WORKFLOW.second, 2);
        client.publish("signal", WORKFLOW.third, (Void) null);
        client.publish("signal", WORKFLOW.signalMap, "one", 5);
        client.skipTimer(
                "signal",
                new StepExecutionId("SignalCombinationStep"),
                TimerId.byConditionId("test-timer-id"));
        final Integer output = client.waitForFlow("signal", Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
