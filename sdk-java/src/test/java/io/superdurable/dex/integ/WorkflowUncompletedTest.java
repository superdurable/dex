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
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowErrorType;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.FlowTimeoutPolicy;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;
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
import static org.junit.jupiter.api.Assertions.assertTrue;

@Tag("dex-dev")
public final class WorkflowUncompletedTest {
    private static final SignalWorkflow WAIT_TIMEOUT_WORKFLOW = new SignalWorkflow();
    private static final WorkflowUncompletedForceFailWorkflow FORCE_FAIL_WORKFLOW =
            new WorkflowUncompletedForceFailWorkflow();
    private static final WorkflowUncompletedStateFailureWorkflow STATE_FAILURE_WORKFLOW =
            new WorkflowUncompletedStateFailureWorkflow();
    private static final WorkflowUncompletedStateTimeoutWorkflow STATE_TIMEOUT_WORKFLOW =
            new WorkflowUncompletedStateTimeoutWorkflow();
    private static final WorkflowUncompletedEmptyDecisionWorkflow EMPTY_DECISION_WORKFLOW =
            new WorkflowUncompletedEmptyDecisionWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testFlowWaitTimeout() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WAIT_TIMEOUT_WORKFLOW)) {
            final String flowId = flowId("wait-timeout");
            environment.client().startFlow(WAIT_TIMEOUT_WORKFLOW, flowId, 1);

            final LongPollTimeoutException failure = assertThrows(
                    LongPollTimeoutException.class,
                    () -> environment.client().waitForFlow(flowId, Duration.ofSeconds(1)).getSingleOutput(Integer.class));
            assertEquals(flowId, failure.getFlowId());
        }
    }

    @Test
    void testFlowTimeout() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WAIT_TIMEOUT_WORKFLOW)) {
            final String flowId = flowId("flow-timeout");
            environment.client().startFlow(
                    WAIT_TIMEOUT_WORKFLOW,
                    flowId,
                    1,
                    StartFlowOptions.newBuilder().timeout(Duration.ofSeconds(1)).build());

            final FlowResult failure = waitForFailure(environment, flowId);
            assertFailure(
                    failure,
                    FlowStatus.FAILED,
                    FlowErrorType.FLOW_TIMEOUT,
                    "Flow timed out after 1 seconds",
                    0);
        }
    }

    @Test
    void testFlowTimeoutHandler() throws Exception {
        final TimeoutHandlerFlow flow = new TimeoutHandlerFlow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                flow)) {
            final String flowId = flowId("flow-timeout-handler");
            environment.client().startFlow(
                    flow,
                    flowId,
                    null,
                    StartFlowOptions.newBuilder().timeout(Duration.ofSeconds(1)).build());

            assertEquals(
                    "expired",
                    environment.client()
                            .waitForFlow(flowId, Duration.ofSeconds(15))
                            .getSingleOutput(String.class));
        }
    }

    @Test
    void testFlowTimeoutHandlerCancelOverride() throws Exception {
        final TimeoutHandlerFlow flow = new TimeoutHandlerFlow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                flow)) {
            final String flowId = flowId("flow-timeout-handler-cancel");
            environment.client().startFlow(
                    flow,
                    flowId,
                    null,
                    StartFlowOptions.newBuilder()
                            .timeout(Duration.ofSeconds(1))
                            .timeoutPolicy(FlowTimeoutPolicy.CANCEL)
                            .build());

            assertFailure(
                    waitForFailure(environment, flowId),
                    FlowStatus.CANCELED,
                    null,
                    null,
                    0);
        }
    }

    @Test
    void testFlowTimeoutHandlerRequiresOverride() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WAIT_TIMEOUT_WORKFLOW)) {
            final IllegalArgumentException missingTimeout = assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().startFlow(
                            WAIT_TIMEOUT_WORKFLOW,
                            flowId("flow-timeout-policy-without-timeout"),
                            1,
                            StartFlowOptions.newBuilder()
                                    .timeoutPolicy(FlowTimeoutPolicy.CANCEL)
                                    .build()));
            assertTrue(missingTimeout.getMessage().contains("requires a positive timeout"));

            final IllegalArgumentException failure = assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().startFlow(
                            WAIT_TIMEOUT_WORKFLOW,
                            flowId("flow-timeout-handler-missing"),
                            1,
                            StartFlowOptions.newBuilder()
                                    .timeout(Duration.ofSeconds(1))
                                    .timeoutPolicy(FlowTimeoutPolicy.HANDLER)
                                    .build()));
            assertTrue(failure.getMessage().contains("does not override handleTimeout"));
        }
    }

    @Test
    void testFlowCanceled() throws Exception {
        assertStoppedFlow(StopType.CANCEL, null, FlowStatus.CANCELED, null, null);
    }

    @Test
    void testFlowTerminated() throws Exception {
        assertStoppedFlow(
                StopType.TERMINATE,
                "terminated",
                FlowStatus.TERMINATED,
                null,
                null);
    }

    @Test
    void testFlowFailedByApi() throws Exception {
        assertStoppedFlow(
                StopType.FAIL,
                "fail by API",
                FlowStatus.FAILED,
                FlowErrorType.CLIENT_API_FAILED,
                "fail by API");
    }

    @Test
    void testForceFailFlow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                FORCE_FAIL_WORKFLOW)) {
            final String flowId = flowId("force-fail");
            environment.client().startFlow(
                    FORCE_FAIL_WORKFLOW,
                    flowId,
                    5);

            final FlowResult failure = waitForFailure(environment, flowId);
            assertFailure(
                    failure,
                    FlowStatus.FAILED,
                    FlowErrorType.STEP_DECISION_FAILED,
                    "a failing message",
                    0);
        }
    }

    @Test
    void testWorkerApiFailure() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                STATE_FAILURE_WORKFLOW)) {
            final String flowId = flowId("worker-api-failure");
            environment.client().startFlow(
                    STATE_FAILURE_WORKFLOW,
                    flowId,
                    5);

            final FlowResult failure = waitForFailure(environment, flowId);
            assertEquals(FlowStatus.FAILED, failure.getStatus());
            assertEquals(FlowErrorType.WORKER_API_FAILED, failure.getErrorType());
            assertTrue(failure.getErrorMessage().contains("test api failing"), failure.getErrorMessage());
            assertEquals(0, failure.getCompletions().size());
        }
    }

    @Test
    void testWorkerApiTimeout() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                STATE_TIMEOUT_WORKFLOW)) {
            final String flowId = flowId("worker-api-timeout");
            environment.client().startFlow(
                    STATE_TIMEOUT_WORKFLOW,
                    flowId,
                    5);

            final FlowResult failure = waitForFailure(environment, flowId);
            assertEquals(FlowStatus.FAILED, failure.getStatus());
            assertEquals(FlowErrorType.WORKER_API_FAILED, failure.getErrorType());
            assertTrue(failure.getErrorMessage().toLowerCase().contains("timeout"), failure.getErrorMessage());
            assertEquals(0, failure.getCompletions().size());
        }
    }

    @Test
    void testEmptyDecisionFailsFlow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                EMPTY_DECISION_WORKFLOW)) {
            final String flowId = flowId("empty-decision");
            environment.client().startFlow(
                    EMPTY_DECISION_WORKFLOW,
                    flowId,
                    5);

            final FlowResult failure = waitForFailure(environment, flowId);
            assertEquals(FlowStatus.FAILED, failure.getStatus());
            assertEquals(FlowErrorType.WORKER_API_FAILED, failure.getErrorType());
            assertTrue(
                    failure.getErrorMessage().contains("goToMulti requires a movement"),
                    failure.getErrorMessage());
            assertEquals(0, failure.getCompletions().size());
        }
    }

    private void assertStoppedFlow(
            final StopType stopType,
            final String reason,
            final FlowStatus expectedStatus,
            final FlowErrorType expectedErrorType,
            final String expectedMessage) throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WAIT_TIMEOUT_WORKFLOW)) {
            final String flowId = flowId("stopped");
            environment.client().startFlow(
                    WAIT_TIMEOUT_WORKFLOW,
                    flowId,
                    1);
            environment.client().stopFlow(flowId, new StopFlowOptions(stopType, reason));

            final FlowResult failure = waitForFailure(environment, flowId);
            assertFailure(
                    failure,
                    expectedStatus,
                    expectedErrorType,
                    expectedMessage,
                    0);
        }
    }

    private static FlowResult waitForFailure(
            final DexDevTestEnvironment environment,
            final String flowId) {
        return environment.client().waitForFlow(flowId, Duration.ofSeconds(15));
    }

    private static void assertFailure(
            final FlowResult failure,
            final FlowStatus status,
            final FlowErrorType errorType,
            final String message,
            final int resultCount) {
        assertEquals(status, failure.getStatus());
        assertEquals(errorType, failure.getErrorType());
        if (message == null) {
            assertNull(failure.getErrorMessage());
        } else {
            assertEquals(message, failure.getErrorMessage());
        }
        assertEquals(resultCount, failure.getCompletions().size());
    }

    private static String flowId(final String prefix) {
        return prefix + "-" + UUID.randomUUID();
    }

    private static final class TimeoutHandlerFlow implements Flow<Void> {
        @Override
        public StepDecision handleTimeout(final Context context) {
            return StepDecision.forceComplete("expired");
        }
    }
}
