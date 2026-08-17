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
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.TimeTravelOptions;
import io.superdurable.dex.TimeTravelStepMethod;
import io.superdurable.dex.TimeTravelType;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepCompletion;
import io.superdurable.dex.exceptions.FlowNotFoundException;
import io.superdurable.dex.testing.DexDevTestEnvironment;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Path;
import java.time.Duration;
import java.util.Arrays;
import java.util.HashSet;
import java.util.Set;
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
                    () -> environment.client().timeTravel(
                            "missing-reset-" + UUID.randomUUID(),
                            TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING).build()));
        }
    }

    @Test
    void testResetWithLockingReappliesRpc() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, true);
            assertCompletedWithAttributes(environment, flowId, true);

            final String resetRunId = environment.client().timeTravel(
                    flowId,
                    resetOptions(false));

            assertCompletedWithAttributes(environment, flowId, true);
            assertEquals(resetRunId, environment.client().describeFlow(flowId).getRunId());
        }
    }

    @Test
    void testResetWithLockingCanSkipRpcReapply() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, true);
            assertCompletedWithAttributes(environment, flowId, true);

            final String resetRunId = environment.client().timeTravel(
                    flowId,
                    resetOptions(true));

            assertEquals(resetRunId, environment.client().describeFlow(flowId).getRunId());
            assertResetTimesOutWithoutAttributes(environment, flowId);
        }
    }

    @Test
    void testResetWithoutLockingReappliesChannelRpc() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, false);
            assertCompletedWithAttributes(environment, flowId, false);

            final String resetRunId = environment.client().timeTravel(
                    flowId,
                    resetOptions(false));

            assertCompletedWithAttributes(environment, flowId, false);
            assertEquals(resetRunId, environment.client().describeFlow(flowId).getRunId());
        }
    }

    @Test
    void testResetWithoutLockingCanSkipChannelReapply() throws Exception {
        try (DexDevTestEnvironment environment = startEnvironment()) {
            final String flowId = startAndInvoke(environment, false);
            assertCompletedWithAttributes(environment, flowId, false);

            final String resetRunId = environment.client().timeTravel(
                    flowId,
                    resetOptions(true));

            assertEquals(resetRunId, environment.client().describeFlow(flowId).getRunId());
            assertResetTimesOutWithoutAttributes(environment, flowId);
        }
    }

    void compileLockingRpcReapply(final Client client) {
        client.startFlow(WORKFLOW, "reset-locking", null);
        final ResetWorkflow stub = client.newRpcStub(
                ResetWorkflow.class,
                "reset-locking");
        client.invokeRPC(stub::withLocking);
        client.invokeRPC(stub::withAttributeMapLock);
        final TimeTravelOptions options = TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING)
                .reason("replay locking RPC")
                .skipWritesReapply(false)
                .build();
        final String runId = client.timeTravel("reset-locking", options);
        consume(runId);
    }

    void compileSkipWritesReapply(final Client client) {
        final TimeTravelOptions options = TimeTravelOptions.newBuilder(TimeTravelType.STEP_TYPE)
                .stepType("LockWaitStep")
                .skipWritesReapply(true)
                .build();
        final String runId = client.timeTravel("reset-locking", options);
        consume(runId);

        final TimeTravelOptions stepExecutionOptions = TimeTravelOptions
                .newBuilder(TimeTravelType.STEP_EXECUTION_ID)
                .stepExecutionId("LockWaitStep-1")
                .stepMethod(TimeTravelStepMethod.EXECUTE)
                .build();
        consume(client.timeTravel("reset-locking", stepExecutionOptions));
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

    private static TimeTravelOptions resetOptions(final boolean skipWrites) {
        return TimeTravelOptions.newBuilder(TimeTravelType.BEGINNING)
                .reason("testing reset")
                .skipWritesReapply(skipWrites)
                .build();
    }

    private static void assertCompletedWithAttributes(
            final DexDevTestEnvironment environment,
            final String flowId,
            final boolean expectsAttributeMapValue) {
        assertEquals(
                new HashSet<Integer>(Arrays.asList(1, 2)),
                completionOutputs(environment.client().waitForFlow(
                        flowId,
                        Duration.ofSeconds(10))));
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
            final String flowId) {
        final FlowResult failure =
                environment.client().waitForFlow(flowId, Duration.ofSeconds(10));
        assertEquals(FlowStatus.FAILED, failure.getStatus());
        assertEquals(0, failure.getCompletions().size());
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.data));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.keyword));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.counter));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.executionCount));
        assertNull(environment.client().getAttribute(flowId, WORKFLOW.items, "order-1"));
    }

    private static void consume(final Object value) {
    }

    private static Set<Integer> completionOutputs(final FlowResult result) {
        final Set<Integer> outputs = new HashSet<Integer>();
        for (final StepCompletion completion : result.getCompletions()) {
            outputs.add(completion.getOutput(Integer.class));
        }
        assertEquals(2, result.getCompletions().size());
        return outputs;
    }
}
