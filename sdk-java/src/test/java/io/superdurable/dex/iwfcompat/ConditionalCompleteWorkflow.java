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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;

class ConditionalCompleteWorkflow implements Flow<Boolean> {
    final Channel<Void> signal = Channel.define("test-signal-channel", Void.class);
    final Channel<Void> internal = Channel.define("test-internal-channel", Void.class);
    private final Attribute<Integer> counter = Attribute.define("counter", Integer.class);
    private final ConditionalStep start = new ConditionalStep();

    @Override
    public StepList<Boolean> getSteps() {
        return StepList.startStep(start);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(counter, signal, internal);
    }

    @RPC
    public void publishToInternalChannel(final Context context) {
        internal.publish(context, null);
    }

    final class ConditionalStep implements Step<Boolean> {
        @Override
        public Class<Boolean> getInputType() {
            return Boolean.class;
        }

        @Override
        public Wait waitFor(final Context context, final Boolean useSignal) {
            return Wait.anyOf((useSignal ? signal : internal).forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Boolean useSignal) {
            final int next = counter.get(context) + 1;
            counter.set(context, next);
            final Channel<Void> selected = useSignal ? signal : internal;
            return StepDecision.forceCompleteWhenChannelsEmpty(
                    next,
                    StepMovement.of(this, useSignal),
                    selected);
        }
    }
}
