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

import io.superdurable.dex.Flow;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;

@Tag("dex-dev")
public final class NonRetryableTest {
    /** Far below the hourly retry interval: completing in time proves no retry ran. */
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(30);

    @TempDir
    Path cacheDirectory;

    @Test
    void nonRetryableExecuteFailureSkipsRetriesAndProceedsToRecoveryStep() throws Exception {
        assertRecoveredAfterOneAttempt(new NonRetryableWorkflows.ExecuteWorkflow(), "non-retryable-execute");
    }

    @Test
    void nonRetryableWaitForFailureSkipsRetriesAndProceedsToExecute() throws Exception {
        assertRecoveredAfterOneAttempt(new NonRetryableWorkflows.WaitForWorkflow(), "non-retryable-wait-for");
    }

    private void assertRecoveredAfterOneAttempt(final Flow<String> flow, final String prefix) throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(cacheDirectory, flow)) {
            final String flowId = prefix + "-" + UUID.randomUUID();
            environment.client().startFlow(flow, flowId, "order");
            final String output = environment.client()
                    .waitForFlow(flowId, WAIT_TIMEOUT)
                    .getSingleOutput(String.class);
            assertEquals(
                    NonRetryableWorkflows.CardRejectedException.class.getName() + ": " + NonRetryableWorkflows.DETAIL,
                    output);
            assertEquals(1, NonRetryableWorkflows.ATTEMPTS_BY_FLOW_ID.get(flowId));
        }
    }
}
