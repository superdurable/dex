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

import io.superdurable.dex.ActiveStepSearchMode;
import io.superdurable.dex.Client;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.WorkerTarget;

import java.time.Duration;

public final class BasicTest {
    void compileBasicAndReuse(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(10))
                .idReusePolicy(IdReusePolicy.ALLOW_IF_NOT_RUNNING)
                .build();
        client.startFlow(IwfFlows.BASIC, "basic", 10, options);
        final Integer output = client.waitForFlow("basic", Integer.class);
        client.startFlow(IwfFlows.ABNORMAL_EXIT, "abnormal", 10, options);
        client.startFlow(IwfFlows.BASIC, "abnormal", output, options);
    }

    void compileEmptyAndModelInputs(final Client client) {
        client.startFlow(IwfFlows.EMPTY_INPUT, "empty", null);
        final IwfFlows.ModelInput input = new IwfFlows.ModelInput();
        input.value = 10;
        client.startFlow(IwfFlows.MODEL_INPUT, "model", input);
    }

    void compileFailurePolicyAndConfigOverride(final Client client) {
        final FlowConfig config = FlowConfig.newBuilder()
                .activeStepSearchMode(ActiveStepSearchMode.ALL)
                .workerTarget(new WorkerTarget("worker:8803", false))
                .build();
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .configOverride(config)
                .build();
        client.startFlow(IwfFlows.PROCEED_ON_WAIT_FAILURE, "recover", "input", options);
        client.startFlow(IwfFlows.MIXED_WAIT, "mixed", 0, options);
        client.updateFlowConfig("mixed", config);
    }

    void compileDescribeAndStepWait(final Client client) {
        final FlowInfo info = client.describeFlow("basic");
        client.waitForStepCompletion(
                "basic",
                new StepExecutionId("BasicSecondStep"),
                Duration.ofSeconds(5));
        consume(info);
    }

    private static void consume(final Object value) {
    }
}
