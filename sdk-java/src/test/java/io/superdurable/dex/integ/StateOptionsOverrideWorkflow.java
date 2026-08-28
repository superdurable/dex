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
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForFailurePolicy;

final class StateOptionsOverrideWorkflow implements Flow<String> {
    private final OverrideFirstStep first = new OverrideFirstStep();
    private final CompleteStep second = new CompleteStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(first).otherSteps(second);
    }

    final class OverrideFirstStep implements Step<String> {
        private String output;

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            output = input + "_state1_start";
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final StepOptions options = StepOptions.newBuilder()
                    .waitForRetry(RetryPolicy.newBuilder().maximumAttempts(2).build())
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
            output += "_state1_decide";
            return StepDecision.goToMany(
                    StepMovement.of(CompleteStep.class, output, options));
        }
    }

    static final class CompleteStep implements Step<String> {
        private String output;

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            output = input + "_state2_start";
            throw new IllegalStateException("state 2 wait failure");
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            if (!context.waitForMethodFailed()) {
                throw new IllegalStateException("waitFor failure was not reported");
            }
            output += "_state2_decide";
            return StepDecision.gracefulComplete(output);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .waitForRetry(RetryPolicy.newBuilder().maximumAttempts(1).build())
                    .waitForFailure(WaitForFailurePolicy.FAIL_FLOW)
                    .build();
        }
    }
}
