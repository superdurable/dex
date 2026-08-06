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
    private static final BasicWorkflow WORKFLOW = new BasicWorkflow();
    private static final BasicAbnormalExitWorkflow ABNORMAL_EXIT_WORKFLOW =
            new BasicAbnormalExitWorkflow();
    private static final BasicEmptyInputWorkflow EMPTY_INPUT_WORKFLOW =
            new BasicEmptyInputWorkflow();
    private static final BasicModelInputWorkflow MODEL_INPUT_WORKFLOW =
            new BasicModelInputWorkflow();
    private static final BasicProceedOnWaitFailureWorkflow WAIT_FAILURE_WORKFLOW =
            new BasicProceedOnWaitFailureWorkflow();
    private static final SkipWaitUntilMixedWaitWorkflow MIXED_WAIT_WORKFLOW =
            new SkipWaitUntilMixedWaitWorkflow();

    void compileBasicAndReuse(final Client client) {
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .timeout(Duration.ofSeconds(10))
                .idReusePolicy(IdReusePolicy.ALLOW_IF_NOT_RUNNING)
                .build();
        client.startFlow(WORKFLOW, "basic", 10, options);
        final Integer output = client.waitForFlow("basic", Integer.class);
        client.startFlow(ABNORMAL_EXIT_WORKFLOW, "abnormal", 10, options);
        client.startFlow(WORKFLOW, "abnormal", output, options);
    }

    void compileEmptyAndModelInputs(final Client client) {
        client.startFlow(EMPTY_INPUT_WORKFLOW, "empty", null);
        final BasicModelInputWorkflow.Input input = new BasicModelInputWorkflow.Input();
        input.value = 10;
        client.startFlow(MODEL_INPUT_WORKFLOW, "model", input);
    }

    void compileFailurePolicyAndConfigOverride(final Client client) {
        final FlowConfig config = FlowConfig.newBuilder()
                .activeStepSearchMode(ActiveStepSearchMode.ALL)
                .workerTarget(new WorkerTarget("worker:8803", false))
                .build();
        final StartFlowOptions options = StartFlowOptions.newBuilder()
                .configOverride(config)
                .build();
        client.startFlow(WAIT_FAILURE_WORKFLOW, "recover", "input", options);
        client.startFlow(MIXED_WAIT_WORKFLOW, "mixed", 0, options);
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
