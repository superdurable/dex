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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;

import java.util.Collections;
import java.util.List;

final class ConditionalCompleteFlow implements Flow<Boolean> {
    final Channel<Void> signal = Channel.define("test-signal-channel", Void.class);
    final Channel<Void> internal = Channel.define("test-internal-channel", Void.class);
    private final Attribute<Integer> counter = Attribute.define("counter", Integer.class);
    private final ConditionalStep start = new ConditionalStep();

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.startStep(start));
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
