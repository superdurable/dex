/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

package io.superdurable.dex.iwfcompat;

import io.superdurable.dex.Channel;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;
import java.util.Collections;
import java.util.List;

final class AnyCombinationFailFlow implements Flow<Integer> {
    private final Channel<Integer> first = Channel.define("test-signal-1", Integer.class);
    private final Channel<Integer> second = Channel.define("test-signal-2", Integer.class);
    private final Channel<Integer> third = Channel.define("test-signal-3", Integer.class);
    private final Step<Integer> start = new Step<Integer>() {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyCombinationOf(
                    ConditionCombination.of(
                            first.forOne("test-signal-1"),
                                Timer.byDuration(Duration.ofSeconds(1), "test-timer-id")),
                    ConditionCombination.of(
                            second.forOne("test-signal-2"),
                            third.forOne("test-signal-3")));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .waitForMethodTimeout(Duration.ofSeconds(1))
                    .build();
        }
    };

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.startStep(start));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(first, second, third);
    }
}
