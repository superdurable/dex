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
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForFailurePolicy;

final class BasicImmutableStepOptionsWorkflow implements Flow<Integer> {
    private final StartStep start = new StartStep();
    private final FailingWaitStep failingWait = new FailingWaitStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start).otherSteps(failingWait);
    }

    final class StartStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final StepOptions override = StepOptions.newBuilder()
                    .waitForRetry(RetryPolicy.newBuilder().maximumAttempts(1).build())
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
            return StepDecision.goToMulti(
                    StepMovement.of(FailingWaitStep.class, 1, override));
        }
    }

    final class FailingWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            throw new IllegalStateException("expected wait failure " + input);
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            if (!context.waitForMethodFailed()) {
                throw new IllegalStateException("wait failure was not reported");
            }
            if (input == 1) {
                return StepDecision.goTo(FailingWaitStep.class, 2);
            }
            return StepDecision.gracefulComplete(input);
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
