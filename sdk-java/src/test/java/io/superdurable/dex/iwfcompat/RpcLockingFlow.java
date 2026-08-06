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
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCAttributeMapLock;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.Wait;

import java.util.Arrays;
import java.util.List;

final class RpcLockingFlow implements Flow<Void> {
    final Channel<Void> channel = Channel.define("rpc-channel", Void.class);
    final Attribute<String> data = Attribute.define("rpc-lock-data", String.class);
    final Attribute<Integer> counter = Attribute.define(
            "rpc-lock-counter",
            Integer.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    final AttributeMap<String> items = AttributeMap.define("rpc-lock-items", String.class);
    private final LockWaitStep first = new LockWaitStep();
    private final LockCompleteStep second = new LockCompleteStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(first),
                StepDef.nonStartStep(second));
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(data, counter, items, channel);
    }

    @RPC(lockAttributes = {"rpc-lock-data", "rpc-lock-counter"})
    public void withLocking(final Context context) {
        data.set(context, "locked");
        counter.set(context, 1);
        channel.publish(context, null);
    }

    @RPC(lockAttributeMaps = {
        @RPCAttributeMapLock(attribute = "rpc-lock-items", instance = "order-1")
    })
    public void withAttributeMapLock(final Context context) {
        items.set(context, "order-1", "locked");
    }

    @RPC
    public void withoutLocking(final Context context) {
        channel.publish(context, null);
    }

    final class LockWaitStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(channel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(second, null);
        }
    }

    static final class LockCompleteStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete("lock complete");
        }
    }
}
