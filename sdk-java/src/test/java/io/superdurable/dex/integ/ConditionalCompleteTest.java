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
public final class ConditionalCompleteTest {
    private static final ConditionalCompleteWorkflow WORKFLOW =
            new ConditionalCompleteWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testSignalChannel() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "conditional-signal-" + UUID.randomUUID();
            environment.client().startFlow(WORKFLOW, flowId, true);
            environment.client().publish(
                    flowId,
                    WORKFLOW.signal,
                    (Void) null,
                    (Void) null,
                    (Void) null);
            assertEquals(3, environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(Integer.class));
        }
    }

    @Test
    void testInternalChannel() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "conditional-internal-" + UUID.randomUUID();
            environment.client().startFlow(WORKFLOW, flowId, false);
            final ConditionalCompleteWorkflow stub = environment.client().newRpcStub(
                    ConditionalCompleteWorkflow.class,
                    flowId);
            environment.client().invokeRPC(stub::publishToInternalChannel, 3);
            assertEquals(3, environment.client().waitForFlow(flowId, Duration.ofSeconds(30)).getSingleOutput(Integer.class));
        }
    }

    void compileSignalChannel(final Client client) {
        client.startFlow(WORKFLOW, "conditional-signal", true);
        client.publish(
                "conditional-signal",
                WORKFLOW.signal,
                (Void) null,
                (Void) null,
                (Void) null);
        final Integer output = client.waitForFlow("conditional-signal").getSingleOutput(Integer.class);
        consume(output);
    }

    void compileInternalChannel(final Client client) {
        client.startFlow(WORKFLOW, "conditional-internal", false);
        final ConditionalCompleteWorkflow stub = client.newRpcStub(
                ConditionalCompleteWorkflow.class,
                "conditional-internal");
        client.invokeRPC(stub::publishToInternalChannel, 3);
    }

    private static void consume(final Object value) {
    }
}
