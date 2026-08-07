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
import java.util.Arrays;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;

@Tag("dex-dev")
public final class InternalChannelTest {
    private static final InternalChannelWorkflow WORKFLOW = new InternalChannelWorkflow();
    private static final InternalChannelWaitingWorkflow WAITING_WORKFLOW =
            new InternalChannelWaitingWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testBasicInternalChannel() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "basic-internal-" + UUID.randomUUID();
            environment.client().startFlow(WORKFLOW, flowId, 1);
            assertEquals(2, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testWaitingInternalChannel() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WAITING_WORKFLOW)) {
            final String flowId = "waiting-internal-" + UUID.randomUUID();
            environment.client().startFlow(WAITING_WORKFLOW, flowId, 1);
            environment.client().publish(
                    flowId,
                    WAITING_WORKFLOW.channel,
                    Arrays.asList(2, 3));
            assertEquals(6, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
        }
    }

    void compileBasicInternalChannel(final Client client) {
        client.startFlow(WORKFLOW, "basic-internal", 1);
        final Integer output = client.waitForFlow("basic-internal", Integer.class);
        consume(output);
    }

    void compileWaitingInternalChannel(final Client client) {
        client.startFlow(WAITING_WORKFLOW, "waiting-internal", 1);
        client.publish(
                "waiting-internal",
                WAITING_WORKFLOW.channel,
                Arrays.asList(2, 3));
        final Integer output = client.waitForFlow("waiting-internal", Integer.class);
        consume(output);
    }

    private static void consume(final Object value) {
    }
}
