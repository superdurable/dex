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
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCAttributeMapLock;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.Wait;

class ResetWorkflow implements Flow<Void> {
    final Channel<Void> channel = Channel.define("rpc-channel", Void.class);
    final Attribute<String> data = Attribute.define("rpc-lock-data", String.class);
    final Attribute<String> keyword = Attribute.define(
            "CustomKeywordField",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    final Attribute<Integer> counter = Attribute.define(
            "CustomIntField",
            Integer.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    final AttributeMap<String> items = AttributeMap.define("rpc-lock-items", String.class);
    private final LockWaitStep first = new LockWaitStep();
    private final LockCompleteStep second = new LockCompleteStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(first).otherSteps(second);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(data, keyword, counter, items, channel);
    }

    @RPC(lockAttributes = {"rpc-lock-data", "CustomKeywordField", "CustomIntField"})
    public void withLocking(final Context context) {
        writeAttributes(context);
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
        writeAttributes(context);
        channel.publish(context, null);
    }

    private void writeAttributes(final Context context) {
        data.set(context, "random-string");
        keyword.set(context, "random-string");
        counter.set(context, 100);
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
