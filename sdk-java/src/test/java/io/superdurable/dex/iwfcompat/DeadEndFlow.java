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
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;

import java.util.Collections;
import java.util.List;

final class DeadEndFlow implements Flow<Void> {
    final Channel<Void> idleSignal = Channel.define("idle-signal", Void.class);
    private final Step<Void> start = new Step<Void>() {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.deadEnd();
        }
    };

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.startStep(start));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(idleSignal);
    }

    @RPC
    public RPCResult<Integer> signalSize(final Context context) {
        return RPCResult.of(idleSignal.size(context));
    }

    @RPC
    public RPCResult<Integer> publishInternal(final Context context) {
        idleSignal.publish(context, null);
        return RPCResult.of(idleSignal.size(context));
    }
}
