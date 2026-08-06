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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForFailurePolicy;

import java.util.Arrays;
import java.util.List;

final class ProceedOnWaitFailureFlow implements Flow<String> {
    private final FailingWaitStep first = new FailingWaitStep();
    private final IwfFlows.CompleteStringStep second = new IwfFlows.CompleteStringStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(first),
                StepDef.nonStartStep(second));
    }

    final class FailingWaitStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            throw new IllegalStateException("wait failure");
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.goTo(second, input + "-recovered");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
        }
    }
}
