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

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Client;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StopFlowOptions;
import io.superdurable.dex.StopType;

import java.time.Duration;

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

    void compileWaitAndFlowTimeouts(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(1))
                .build();
        client.startFlow(WAIT_TIMEOUT_WORKFLOW, "wait-timeout", 0, options);
        final Integer output = client.waitForFlow(
                "wait-timeout",
                Integer.class,
                Duration.ofMillis(1));
        consume(output);
    }

    void compileCancellationTerminationAndFailure(final Client client) {
        client.stopFlow("cancel");
        client.stopFlow(
                "terminate",
                new StopFlowOptions(StopType.TERMINATE, "terminated"));
        client.stopFlow(
                "fail",
                new StopFlowOptions(StopType.FAIL, "failed by API"));
    }

    void compileWorkerFailureModes(final Client client) {
        client.startFlow(FORCE_FAIL_WORKFLOW, "force-fail", 0);
        client.startFlow(STATE_FAILURE_WORKFLOW, "state-failure", 0);
        client.startFlow(STATE_TIMEOUT_WORKFLOW, "state-timeout", 0);
        client.startFlow(EMPTY_DECISION_WORKFLOW, "empty-decision", 0);
    }

    private static void consume(final Object value) {
    }
}
