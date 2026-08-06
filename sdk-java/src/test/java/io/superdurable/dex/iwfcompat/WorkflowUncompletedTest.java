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
    void compileWaitAndFlowTimeouts(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(1))
                .build();
        client.startFlow(IwfFlows.SIGNAL, "wait-timeout", 0, options);
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
        client.startFlow(IwfFlows.FORCE_FAIL, "force-fail", 0);
        client.startFlow(IwfFlows.STATE_FAILURE, "state-failure", 0);
        client.startFlow(IwfFlows.STATE_TIMEOUT, "state-timeout", 0);
        client.startFlow(IwfFlows.EMPTY_DECISION, "empty-decision", 0);
    }

    private static void consume(final Object value) {
    }
}
