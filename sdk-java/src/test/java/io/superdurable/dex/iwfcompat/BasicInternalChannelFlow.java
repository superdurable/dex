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
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.ConditionCombination;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;

import java.util.Arrays;
import java.util.List;

final class BasicInternalChannelFlow implements Flow<Integer> {
    private final Channel<Integer> firstChannel =
            Channel.define("test-inter-state-channel-1", Integer.class);
    private final ChannelMap<Integer> channelMap =
            ChannelMap.define("test-inter-state-channel-map", Integer.class);
    private final ForkStep start = new ForkStep();
    private final ConsumeStep consumer = new ConsumeStep();
    private final PublishStep publisher = new PublishStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(start),
                StepDef.nonStartStep(consumer),
                StepDef.nonStartStep(publisher));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(firstChannel, channelMap);
    }

    final class ForkStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goToMulti(
                    StepMovement.of(consumer, input),
                    StepMovement.of(publisher, input));
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
                    ConditionCombination.of(firstChannel.forOne("first")),
                    ConditionCombination.of(channelMap.forOne("one")));
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(
                    input + firstChannel.getConditionResults(context).size());
        }
    }

    final class PublishStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            firstChannel.publish(context, input);
            channelMap.publish(context, "one", input);
            return StepDecision.deadEnd();
        }
    }
}
