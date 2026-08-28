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
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;

final class InternalChannelWorkflow implements Flow<Integer> {
    private final Channel<Integer> firstChannel =
            Channel.define("test-inter-state-channel-1", Integer.class);
    private final Channel<Integer> secondChannel =
            Channel.define("test-inter-state-channel-2", Integer.class);
    private final ChannelMap<Integer> channelMap =
            ChannelMap.define("test-inter-state-channel-map", Integer.class);
    private final ForkStep start = new ForkStep();
    private final ConsumeStep consumer = new ConsumeStep();
    private final PublishStep publisher = new PublishStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(start).otherSteps(consumer, publisher);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(firstChannel, secondChannel, channelMap);
    }

    final class ForkStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
        return StepDecision.goToMany(
                    StepMovement.of(ConsumeStep.class, input),
                    StepMovement.of(PublishStep.class, input));
        }
    }

    final class ConsumeStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyCombinationOf(
                    ConditionCombination.of(
                            firstChannel.forOne("first"),
                            channelMap.forOne("one", "mapped")),
                    ConditionCombination.of(secondChannel.forOne("second")));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            if (!secondChannel.getConditionResults(context).isEmpty()) {
                throw new IllegalStateException("second channel should still be waiting");
            }
            final Integer firstValue = firstChannel.getConditionResults(context).get(0);
            final Integer mappedValue = channelMap.getConditionResults(context, "one").get(0);
            if (mappedValue.intValue() != 3) {
                throw new IllegalStateException("mapped channel returned " + mappedValue);
            }
            return StepDecision.gracefulComplete(input + firstValue);
        }
    }

    final class PublishStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            firstChannel.publish(context, 2);
            channelMap.publish(context, "one", 3);
            return StepDecision.deadEnd();
        }
    }
}
