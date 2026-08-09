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
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.ResetFlowOptions;
import io.superdurable.dex.ResetType;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.exceptions.FlowUncompletedException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.UUID;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;

@Tag("dex-dev")
public final class ResetTest {
    private static final ResetWorkflow WORKFLOW = new ResetWorkflow();
    private static final String EXPECTED_VALUE = "random-string";

    @TempDir
    Path cacheDirectory;

    @Test
    void testResetMissingFlow() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            assertThrows(
                    FlowNotFoundException.class,
                    () -> environment.client().resetFlow(
                            "missing-reset-" + UUID.randomUUID(),
                            ResetFlowOptions.newBuilder(ResetType.BEGINNING).build()));
        }
    }

    @Test
    void testResetWithLockingReappliesRpc() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, true);
            assertCompletedWithAttributes(environment, flowId, true);

            final String resetRunId = environment.client().resetFlow(
                    flowId,
                    resetOptions(false, false));

            assertCompletedWithAttributes(environment, flowId, true);
            assertEquals(resetRunId, environment.client().describeFlow(flowId).getRunId());
        }
    }

    @Test
    void testResetWithLockingCanSkipRpcReapply() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, true);
            assertCompletedWithAttributes(environment, flowId, true);

            final String resetRunId = environment.client().resetFlow(
                    flowId,
                    resetOptions(true, true));

            assertResetTimesOutWithoutAttributes(environment, flowId, resetRunId);
        }
    }

    @Test
    void testResetWithoutLockingReappliesChannelRpc() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, false);
            assertCompletedWithAttributes(environment, flowId, false);

            final String resetRunId = environment.client().resetFlow(
                    flowId,
                    resetOptions(false, false));

            assertCompletedWithAttributes(environment, flowId, false);
            assertEquals(resetRunId, environment.client().describeFlow(flowId).getRunId());
        }
    }

    @Test
    void testResetWithoutLockingCanSkipChannelReapply() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, false);
            assertCompletedWithAttributes(environment, flowId, false);

            final String resetRunId = environment.client().resetFlow(
                    flowId,
                    resetOptions(true, true));

            assertResetTimesOutWithoutAttributes(environment, flowId, resetRunId);
        }
    }

    void compileLockingRpcReapply(final Client client) {
        client.startFlow(WORKFLOW, "reset-locking", null);
        final ResetWorkflow stub = client.newRpcStub(
                ResetWorkflow.class,
                "reset-locking");
        client.invokeRPC(stub::withLocking);
        client.invokeRPC(stub::withAttributeMapLock);
        final ResetFlowOptions options = ResetFlowOptions.newBuilder(ResetType.BEGINNING)
                .reason("replay locking RPC")
                .skipLockingRpcReapply(false)
                .build();
        final String runId = client.resetFlow("reset-locking", options);
        consume(runId);
    }

    void compileSkipRpcAndChannelReapply(final Client client) {
        final ResetFlowOptions options = ResetFlowOptions.newBuilder(ResetType.STEP_TYPE)
                .stepType("LockWaitStep")
                .skipLockingRpcReapply(true)
                .skipChannelMessagesReapply(true)
                .build();
        final String runId = client.resetFlow("reset-locking", options);
        consume(runId);
    }

    private DexDevTestEnvironment startEnvironment() throws Exception {
        return DexDevTestEnvironment.start(cacheDirectory, WORKFLOW);
    }

    private static String startAndInvoke(
            final DexDevTestEnvironment environment,
            final boolean locking) {
        final String flowId = "reset-" + UUID.randomUUID();
        environment.client().startFlow(
                WORKFLOW,
                flowId,
                null,
                StartFlowOptions.newBuilder().timeout(Duration.ofSeconds(3)).build());
        final ResetWorkflow stub = environment.client().newRpcStub(ResetWorkflow.class, flowId);
        if (locking) {
            environment.client().invokeRPC(stub::withAttributeMapLock);
            environment.client().invokeRPC(stub::withLocking);
        } else {
            environment.client().invokeRPC(stub::withoutLocking);
        }
        return flowId;
    }

    private static ResetFlowOptions resetOptions(
            final boolean skipLockingRpc,
            final boolean skipChannels) {
        return ResetFlowOptions.newBuilder(ResetType.BEGINNING)
                .reason("testing reset")
                .skipLockingRpcReapply(skipLockingRpc)
                .skipChannelMessagesReapply(skipChannels)
                .build();
    }

    private static void assertCompletedWithAttributes(
            final DexDevTestEnvironment environment,
            final String flowId,
            final boolean expectsAttributeMapValue) {
        assertEquals(
                2,
                environment.client().waitForFlow(
                        flowId,
                        Integer.class,
                        Duration.ofSeconds(10)));
        assertEquals(FlowStatus.COMPLETED, environment.client().describeFlow(flowId).getStatus());
        assertEquals(EXPECTED_VALUE, environment.client().getAttribute(flowId, WORKFLOW.data));
        assertEquals(EXPECTED_VALUE, environment.client().getAttribute(flowId, WORKFLOW.keyword));
        assertEquals(100, environment.client().getAttribute(flowId, WORKFLOW.counter));
        assertEquals(2, environment.client().getAttribute(flowId, WORKFLOW.executionCount));
        final String item = environment.client().getAttribute(
                flowId,
                WORKFLOW.items,
                "order-1");
        if (expectsAttributeMapValue) {
            assertEquals("locked", item);
        } else {
            assertNull(item);
        }
    }

    private static void assertResetTimesOutWithoutAttributes(
            final DexDevTestEnvironment environment,
            final String flowId,
            final String resetRunId) {
        final FlowUncompletedException failure = assertThrows(
                FlowUncompletedException.class,
                () -> environment.client().waitForFlow(
                        flowId,
                        Integer.class,
                        Duration.ofSeconds(10)));
        assertEquals(resetRunId, failure.getRunId());
        assertEquals(FlowStatus.TIMED_OUT, failure.getStatus());
        assertEquals(0, failure.getResultCount());
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.data));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.keyword));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.counter));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.executionCount));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.items, "order-1"));
    }

    private static void consume(final Object value) {
    }
}
