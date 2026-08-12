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
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;

final class MultiOutputWorkflow implements Flow<Void> {
    final MultiOutputStringStep stringStep = new MultiOutputStringStep();
    final MultiOutputIntegerStep integerStep = new MultiOutputIntegerStep();
    private final MultiOutputStartStep start =
            new MultiOutputStartStep(stringStep, integerStep);

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(start).otherSteps(stringStep, integerStep);
    }

    static final class MultiOutputStartStep implements Step<Void> {
        private final MultiOutputStringStep stringStep;
        private final MultiOutputIntegerStep integerStep;

        MultiOutputStartStep(
                final MultiOutputStringStep stringStep,
                final MultiOutputIntegerStep integerStep) {
            this.stringStep = stringStep;
            this.integerStep = integerStep;
        }

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMulti(
                    StepMovement.of(stringStep, null),
                    StepMovement.of(integerStep, null));
        }
    }

    static final class MultiOutputStringStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete("branch-one");
        }
    }

    static final class MultiOutputIntegerStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(42);
        }
    }
}
