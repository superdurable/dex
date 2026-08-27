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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;

final class SkipWaitUntilWorkflow implements Flow<Integer> {
    private final ExecuteOnlyFirstStep first = new ExecuteOnlyFirstStep();
    private final ExecuteOnlySecondStep second = new ExecuteOnlySecondStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(first).otherSteps(second);
    }

    final class ExecuteOnlyFirstStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goTo(ExecuteOnlySecondStep.class, input + 1);
        }
    }

    static final class ExecuteOnlySecondStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input + 1);
        }
    }
}
