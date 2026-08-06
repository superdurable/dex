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
