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

import io.superdurable.dex.StreamMessage;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class StreamTest {
    @TempDir
    Path cacheDirectory;

    @Test
    void testStreamRoundTrip() throws Exception {
        final StreamTestWorkflow workflow = new StreamTestWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(cacheDirectory, workflow)) {
            final String flowId = "stream-" + UUID.randomUUID();
            environment.client().startFlow(workflow, flowId, null);
            environment.client().waitForFlow(flowId, Duration.ofSeconds(30));

            environment.client().writeStream(flowId, workflow.progress, "client-write", "client-progress");
            environment.client().writeStream(flowId, workflow.progress, "client-write", "duplicate-retained");

            final StreamMessage<String> step = environment.client()
                    .readStream(flowId, workflow.progress, "", Duration.ofSeconds(30));
            assertEquals("step-progress-1", step.getValue());
            assertFalse(step.getResumeToken().isEmpty());
            assertTrue(step.getCreatedTime().isAfter(Instant.EPOCH));
            assertEquals("#StreamTestStep-1", step.getSource());

            final StreamMessage<String> secondStep = environment.client()
                    .readStream(flowId, workflow.progress, step.getResumeToken(), Duration.ofSeconds(30));
            assertEquals("step-progress-2", secondStep.getValue());
            assertEquals(step.getSource(), secondStep.getSource());

            final StreamMessage<String> client = environment.client()
                    .readStream(flowId, workflow.progress, secondStep.getResumeToken(), Duration.ofSeconds(30));
            assertEquals("client-progress", client.getValue());
            assertFalse(client.getResumeToken().equals(secondStep.getResumeToken()));
            assertTrue(client.getCreatedTime().isAfter(Instant.EPOCH));
            assertEquals("client-write", client.getSource());

            final StreamMessage<String> duplicate = environment.client()
                    .readStream(flowId, workflow.progress, client.getResumeToken(), Duration.ofSeconds(30));
            assertEquals("duplicate-retained", duplicate.getValue());
            assertEquals("client-write", duplicate.getSource());
        }
    }
}
