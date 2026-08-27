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

import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;

final class SignalWorkflow implements Flow<Integer> {
    final Channel<Integer> first = Channel.define("signal-1", Integer.class);
    final Channel<Integer> second = Channel.define("signal-2", Integer.class);
    final Channel<Void> third = Channel.define("signal-3", Void.class);
    final ChannelMap<Integer> signalMap = ChannelMap.define("signal-map", Integer.class);
    private final SignalFirstStep start = new SignalFirstStep();
    private final SignalCombinationStep combination = new SignalCombinationStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start).otherSteps(combination);
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
            return Wait.anyOf(
                    first.forOne("test-signal-id-1"),
                    second.forOne("test-signal-id-2"));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            if (!second.getConditionResults(context).isEmpty()) {
                throw new IllegalStateException("second signal should still be waiting");
            }
            final int value = first.getConditionResults(context).get(0);
            return StepDecision.goTo(SignalCombinationStep.class, input + value);
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
                            first.forOne("signal-1"),
                            third.forOne("signal-3"),
                            signalMap.forOne("one", "signal-map"),
                            Timer.byDuration(Duration.ofDays(365), "test-timer-id")));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            if (!second.getConditionResults(context).isEmpty()) {
                throw new IllegalStateException("second signal should still be waiting");
            }
            if (third.getConditionResults(context).size() != 1) {
                throw new IllegalStateException("null signal was not received");
            }
            if (signalMap.getConditionResults(context, "one").size() != 1) {
                throw new IllegalStateException("mapped signal was not received");
            }
            if (!context.hasTimerFired()) {
                throw new IllegalStateException("timer was not fired");
            }
            return StepDecision.gracefulComplete(
                    input + first.getConditionResults(context).get(0));
        }
    }
}
