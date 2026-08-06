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
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

final class StateOptionsOverrideFlow implements Flow<String> {
    private final OverrideFirstStep first = new OverrideFirstStep();
    private final IwfFlows.CompleteStringStep second = new IwfFlows.CompleteStringStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(first),
                StepDef.nonStartStep(second));
    }

    final class OverrideFirstStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final StepOptions options = StepOptions.newBuilder()
                    .waitForMethodTimeout(Duration.ofSeconds(2))
                    .executeMethodTimeout(Duration.ofSeconds(3))
                    .build();
            return StepDecision.goToMulti(StepMovement.of(second, input, options));
        }
    }
}
