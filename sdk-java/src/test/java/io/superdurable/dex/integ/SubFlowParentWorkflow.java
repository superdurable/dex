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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.FlowConfig;
import io.superdurable.dex.FlowResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.SubFlow;
import io.superdurable.dex.SubFlowOptions;
import io.superdurable.dex.SubFlowReusePolicy;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;

final class SubFlowParentWorkflow implements Flow<Integer> {
    private final ParentStep parentStep = new ParentStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class ParentStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.until(SubFlow.run(BasicWorkflow.class, input));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FlowResult result = SubFlow.getConditionResults(context);
            return StepDecision.gracefulComplete(
                    result.getFlowId()
                            + "|"
                            + result.getRunId()
                            + "|"
                            + result.getSingleOutput(Integer.class));
        }
    }
}

final class SubFlowAllParentWorkflow implements Flow<Integer> {
    private final ParentStep parentStep = new ParentStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class ParentStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.allOf(
                    SubFlow.run(BasicWorkflow.class, input),
                    SubFlow.run(BasicWorkflow.class, input + 10));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FlowResult first = SubFlow.getConditionResults(context);
            final FlowResult second = SubFlow.getConditionResults(context, 1);
            return StepDecision.gracefulComplete(format(first) + ";" + format(second));
        }

        private static String format(final FlowResult result) {
            return result.getFlowId()
                    + "|"
                    + result.getRunId()
                    + "|"
                    + result.getStatus()
                    + "|"
                    + result.getSingleOutput(Integer.class);
        }
    }
}

final class SubFlowAnyParentWorkflow implements Flow<Integer> {
    private final ParentStep parentStep = new ParentStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class ParentStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyOf(
                    Timer.byDuration(Duration.ZERO),
                    SubFlow.run(TimerWorkflow.class, input));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FlowResult result = SubFlow.getConditionResults(context);
            boolean rejectedOutput = false;
            try {
                result.getSingleOutput(Integer.class);
            } catch (IllegalStateException expected) {
                rejectedOutput = true;
            }
            return StepDecision.gracefulComplete(
                    result.getFlowId()
                            + "|"
                            + result.getStatus()
                            + "|"
                            + (result.getRunId() == null)
                            + "|"
                            + result.isTerminal()
                            + "|"
                            + rejectedOutput);
        }
    }
}

abstract class TimerSubFlowParentWorkflow implements Flow<Integer> {
    private final ParentStep parentStep;

    TimerSubFlowParentWorkflow(final SubFlowReusePolicy reusePolicy) {
        parentStep = new ParentStep(reusePolicy);
    }

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class ParentStep implements Step<Integer> {
        private final SubFlowOptions options;

        ParentStep(final SubFlowReusePolicy reusePolicy) {
            options = SubFlowOptions.newBuilder().reusePolicy(reusePolicy).build();
        }

        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.until(SubFlow.run(TimerWorkflow.class, input, options));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FlowResult result = SubFlow.getConditionResults(context);
            return StepDecision.gracefulComplete(
                    result.getFlowId() + "|" + result.getRunId() + "|" + result.getStatus());
        }
    }
}

final class SubFlowAttachParentWorkflow extends TimerSubFlowParentWorkflow {
    SubFlowAttachParentWorkflow() {
        super(SubFlowReusePolicy.ATTACH);
    }
}

final class SubFlowAlwaysRestartParentWorkflow extends TimerSubFlowParentWorkflow {
    SubFlowAlwaysRestartParentWorkflow() {
        super(SubFlowReusePolicy.ALWAYS_RESTART);
    }
}

final class SubFlowAbnormalRestartParentWorkflow implements Flow<Integer> {
    private final ParentStep parentStep = new ParentStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class ParentStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.until(SubFlow.run(BasicAbnormalExitWorkflow.class, input));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FlowResult result = SubFlow.getConditionResults(context);
            return StepDecision.gracefulComplete(
                    result.getFlowId() + "|" + result.getRunId() + "|" + result.getStatus());
        }
    }
}

final class SubFlowContinueAsNewParentWorkflow implements Flow<Integer> {
    private final ParentStep parentStep = new ParentStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(parentStep);
    }

    static final class ParentStep implements Step<Integer> {
        private final SubFlowOptions childOptions = SubFlowOptions.newBuilder()
                .configOverride(FlowConfig.newBuilder().continueAsNewThreshold(100).build())
                .build();

        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.allOf(
                    SubFlow.run(BasicWorkflow.class, input, childOptions),
                    SubFlow.run(TimerWorkflow.class, 300, childOptions));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final FlowResult completed = SubFlow.getConditionResults(context);
            final FlowResult delayed = SubFlow.getConditionResults(context, 1);
            return StepDecision.gracefulComplete(
                    completed.getRunId() + "|" + completed.getSingleOutput(Integer.class)
                            + "|" + delayed.getRunId() + "|" + delayed.getStatus());
        }
    }
}
