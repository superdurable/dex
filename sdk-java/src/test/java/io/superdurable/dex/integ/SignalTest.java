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

import io.superdurable.dex.Client;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.TimerId;
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
            final SignalWorkflow stub = environment.client().newRpcStub(
                    SignalWorkflow.class,
                    flowId);
            environment.client().invokeRPC(stub::publishFirst, 2);
            environment.client().invokeRPC(stub::publishFirst, 3);
            environment.client().invokeRPC(stub::publishFirst, 5);
            environment.client().invokeRPC(stub::publishThird);
            environment.client().invokeRPC(stub::publishMapped, 4);
            IntegrationTestWaits.skipTimerWhenRegistered(
                    environment.client(),
                    flowId,
                    StepExecutionId.of("SignalCombinationStep"),
                    TimerId.byConditionId("test-timer-id"));
            assertEquals(6, environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(Integer.class));
        }
    }

    void compileSignalsAndTimerSkip(final Client client) {
        client.startFlow(WORKFLOW, "signal", 0);
        final SignalWorkflow stub = client.newRpcStub(SignalWorkflow.class, "signal");
        client.invokeRPC(stub::publishFirst, 1);
        client.invokeRPC(stub::publishSecond, 2);
        client.invokeRPC(stub::publishThird);
        client.invokeRPC(stub::publishMapped, 5);
        client.skipTimer(
                "signal",
                StepExecutionId.of("SignalCombinationStep"),
                TimerId.byConditionId("test-timer-id"));
        final Integer output = client.waitForFlow("signal").getSingleOutput(Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
