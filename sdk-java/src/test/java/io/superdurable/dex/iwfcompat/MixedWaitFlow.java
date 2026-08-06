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
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

final class MixedWaitFlow implements Flow<Integer> {
    private final MixedImmediateStep first = new MixedImmediateStep();
    private final MixedTimerStep second = new MixedTimerStep();
    private final StepOptions shared = StepOptions.newBuilder()
            .executeMethodTimeout(Duration.ofSeconds(5))
            .build();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(first),
                StepDef.nonStartStep(second));
    }

    final class MixedImmediateStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goTo(second, input + 1);
        }

        @Override
        public StepOptions getStepOptions() {
            return shared;
        }
    }

    final class MixedTimerStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.allOf(Timer.byDuration(Duration.ofSeconds(1)));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input + 1);
        }

        @Override
        public StepOptions getStepOptions() {
            return shared;
        }
    }
}
