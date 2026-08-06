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

import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

final class SignalFlow implements Flow<Integer> {
    final Channel<Integer> first = Channel.define("signal-1", Integer.class);
    final Channel<Integer> second = Channel.define("signal-2", Integer.class);
    final Channel<Integer> third = Channel.define("signal-3", Integer.class);
    final ChannelMap<Integer> signalMap = ChannelMap.define("signal-map", Integer.class);
    private final SignalFirstStep start = new SignalFirstStep();
    private final SignalCombinationStep combination = new SignalCombinationStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(start),
                StepDef.nonStartStep(combination));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(first, second, third, signalMap);
    }

    final class SignalFirstStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyOf(first.forOne("test-signal-id"));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            final int value = first.getConditionResults(context).get(0);
            return StepDecision.goTo(combination, input + value);
        }
    }

    final class SignalCombinationStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyCombinationOf(
                    ConditionCombination.of(
                            second.forOne("signal-2"),
                            Timer.byDuration(Duration.ofSeconds(10), "test-timer-id")),
                    ConditionCombination.of(
                            third.forN(2),
                            signalMap.forOne("one")));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input + third.size(context));
        }
    }
}
