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

import io.grpc.Status;
import io.superdurable.dex.ActiveStepSearchMode;
import io.superdurable.dex.Client;
import io.superdurable.dex.DexException;
import io.superdurable.dex.ErrorSubStatus;
import io.superdurable.dex.FlowErrorType;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.FlowStatus;
import io.superdurable.dex.FlowUncompletedException;
import io.superdurable.dex.FlowInfo;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.StepExecutionId;
import io.superdurable.dex.WorkerTarget;
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
    private static final BasicImmutableStepOptionsWorkflow IMMUTABLE_OPTIONS_WORKFLOW =
            new BasicImmutableStepOptionsWorkflow();

    @TempDir
    Path cacheDirectory;

    @Test
    void testBasicWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = "basic-" + UUID.randomUUID();
            final Integer input = 0;
            final StartFlowOptions options = StartFlowOptions.newBuilder()
                    .idReusePolicy(IdReusePolicy.DISALLOW)
                    .build();
            environment.client().startFlow(WORKFLOW, flowId, input, options);
            final Integer output = environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30));
            assertEquals(input + 2, output);
            final DexException duplicate = assertThrows(
                    DexException.class,
                    () -> environment.client().startFlow(WORKFLOW, flowId, input, options));
            assertEquals(Status.Code.ALREADY_EXISTS, duplicate.getCode());
            assertEquals(ErrorSubStatus.FLOW_ALREADY_STARTED, duplicate.getSubStatus());
        }
    }

    @Test
    void testBasicWorkflowAbnormalExitReuse() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                ABNORMAL_EXIT_WORKFLOW,
                WORKFLOW)) {
            final String flowId = flowId("abnormal-exit-reuse");
            final StartFlowOptions options = StartFlowOptions.newBuilder()
                    .idReusePolicy(IdReusePolicy.ALLOW_IF_PREVIOUS_FAILED)
                    .build();
            final String failedRun = environment.client().startFlow(
                    ABNORMAL_EXIT_WORKFLOW,
                    flowId,
                    0,
                    options);
            final FlowUncompletedException failure = assertThrows(
                    FlowUncompletedException.class,
                    () -> environment.client().waitForFlow(
                            flowId,
                            Integer.class,
                            Duration.ofSeconds(30)));
            assertEquals(failedRun, failure.getRunId());
            assertEquals(FlowStatus.FAILED, failure.getStatus());
            environment.client().startFlow(WORKFLOW, flowId, 0, options);
            assertEquals(2, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testEmptyInputWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                EMPTY_INPUT_WORKFLOW)) {
            final String flowId = flowId("empty-input");
            environment.client().startFlow(EMPTY_INPUT_WORKFLOW, flowId, null);
            assertNull(environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
            final DexException missing = assertThrows(
                    DexException.class,
                    () -> environment.client().waitForFlow(
                            flowId("missing"),
                            Integer.class,
                            Duration.ofSeconds(1)));
            assertEquals(Status.Code.NOT_FOUND, missing.getCode());
            assertEquals(ErrorSubStatus.FLOW_NOT_EXISTS, missing.getSubStatus());
        }
    }

    @Test
    void testTypeSpecifiedWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                EMPTY_INPUT_WORKFLOW)) {
            final String flowId = flowId("type-specified");
            assertEquals("test-customized-flow-type", EMPTY_INPUT_WORKFLOW.getFlowType());
            environment.client().startFlow(EMPTY_INPUT_WORKFLOW, flowId, null);
            assertNull(environment.client().waitForFlow(
                    flowId,
                    Void.class,
                    Duration.ofSeconds(30)));
            final BasicEmptyInputWorkflow unregistered = new BasicEmptyInputWorkflow();
            assertThrows(
                    IllegalArgumentException.class,
                    () -> environment.client().startFlow(
                            unregistered,
                            flowId("unregistered"),
                            null));
        }
    }

    @Test
    void testModelInputWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                MODEL_INPUT_WORKFLOW)) {
            final String flowId = flowId("model-input");
            final BasicModelInputWorkflow.Input input = new BasicModelInputWorkflow.Input();
            input.value = 10;
            environment.client().startFlow(MODEL_INPUT_WORKFLOW, flowId, input);
            assertEquals(10, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
            assertThrows(
                    IllegalArgumentException.class,
                    () -> startWithWrongInput(environment.client(), flowId("wrong-input")));
        }
    }

    @Test
    void testWorkflowConfigOverride() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("config-override");
            final StartFlowOptions options = StartFlowOptions.newBuilder()
                    .configOverride(FlowConfig.newBuilder()
                            .continueAsNewThreshold(1)
                            .build())
                    .build();
            environment.client().startFlow(WORKFLOW, flowId, 0, options);
            assertEquals(2, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testGetWorkflowStatusWhenNoExistingWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final DexException missing = assertThrows(
                    DexException.class,
                    () -> environment.client().describeFlow(flowId("missing")));
            assertEquals(Status.Code.NOT_FOUND, missing.getCode());
            assertEquals(ErrorSubStatus.FLOW_NOT_EXISTS, missing.getSubStatus());
        }
    }

    @Test
    void testGetWorkflowStatusWhenWorkflowIsRunning() throws Exception {
        final SignalWorkflow waitingWorkflow = new SignalWorkflow();
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                waitingWorkflow)) {
            final String flowId = flowId("running");
            environment.client().startFlow(waitingWorkflow, flowId, 0);
            assertEquals(FlowStatus.RUNNING, environment.client()
                    .describeFlow(flowId)
                    .getStatus());
            environment.client().stopFlow(flowId);
        }
    }

    @Test
    void testWorkflowWaitForStepCompletion() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WORKFLOW)) {
            final String flowId = flowId("wait-step");
            environment.client().startFlow(WORKFLOW, flowId, 5);
            environment.client().waitForStepCompletion(
                    flowId,
                    new StepExecutionId("BasicSecondStep"),
                    Duration.ofSeconds(30));
            assertEquals(7, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testProceedOnWaitFailureWorkflow() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                WAIT_FAILURE_WORKFLOW)) {
            final String flowId = flowId("proceed-on-wait-failure");
            environment.client().startFlow(WAIT_FAILURE_WORKFLOW, flowId, "input");
            assertEquals("input-recovered", environment.client().waitForFlow(
                    flowId,
                    String.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testMixedWaitStyles() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                MIXED_WAIT_WORKFLOW)) {
            final String flowId = flowId("mixed-wait");
            environment.client().startFlow(MIXED_WAIT_WORKFLOW, flowId, 0);
            assertEquals(2, environment.client().waitForFlow(
                    flowId,
                    Integer.class,
                    Duration.ofSeconds(30)));
        }
    }

    @Test
    void testMovementOptionsDoNotMutateStepDefaults() throws Exception {
        try (DexDevTestEnvironment environment = DexDevTestEnvironment.start(
                cacheDirectory,
                IMMUTABLE_OPTIONS_WORKFLOW)) {
            final String flowId = flowId("immutable-options");
            environment.client().startFlow(IMMUTABLE_OPTIONS_WORKFLOW, flowId, 0);

            final FlowUncompletedException failure = assertThrows(
                    FlowUncompletedException.class,
                    () -> environment.client().waitForFlow(
                            flowId,
                            Integer.class,
                            Duration.ofSeconds(30)));
            assertEquals(FlowStatus.FAILED, failure.getStatus());
            assertEquals(FlowErrorType.WORKER_API_FAILED, failure.getErrorType());
            assertEquals("expected wait failure 2", failure.getMessage());
        }
    }

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

    private static String flowId(final String prefix) {
        return prefix + "-" + UUID.randomUUID();
    }

    @SuppressWarnings({"rawtypes", "unchecked"})
    private static void startWithWrongInput(final Client client, final String flowId) {
        client.startFlow((io.superdurable.dex.Flow) MODEL_INPUT_WORKFLOW, flowId, "wrong");
    }
}
