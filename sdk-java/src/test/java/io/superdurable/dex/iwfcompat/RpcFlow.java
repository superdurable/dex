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
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;

class RpcFlow implements Flow<Integer> {
    final Channel<Void> internal = Channel.define("rpc-internal", Void.class);
    final Attribute<String> data = Attribute.define("rpc-data", String.class);
    final Attribute<String> keyword = Attribute.define(
            "rpc-keyword",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    private final RpcFirstStep first = new RpcFirstStep();
    private final RpcSecondStep second = new RpcSecondStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(first).otherSteps(second);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(data, keyword, internal);
    }

    @RPC
    public void noPersistence(final Context context) {
        internal.publish(context, null);
    }

    @RPC
    public RPCResult<Long> functionOne(final Context context, final String input) {
        data.set(context, input);
        keyword.set(context, input);
        return RPCResult.of(1L, StepMovement.of(second, 0));
    }

    @RPC
    public RPCResult<Long> functionZero(final Context context) {
        return RPCResult.of(1L, StepMovement.of(second, 0));
    }

    @RPC
    public void procedureOne(final Context context, final String input) {
        data.set(context, input);
    }

    @RPC
    public void procedureZero(final Context context) {
        internal.publish(context, null);
    }

    @RPC
    public RPCResult<Long> readOnly(final Context context, final String input) {
        return RPCResult.of((long) input.length());
    }

    @RPC
    public void setData(final Context context, final String input) {
        data.set(context, input);
    }

    @RPC
    public RPCResult<String> getData(final Context context) {
        return RPCResult.of(data.get(context));
    }

    @RPC
    public void setKeyword(final Context context, final String input) {
        keyword.set(context, input);
    }

    @RPC
    public RPCResult<String> getKeyword(final Context context) {
        return RPCResult.of(keyword.get(context));
    }

    final class RpcFirstStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.anyOf(internal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goTo(second, 0);
        }
    }

    static final class RpcSecondStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input + 1);
        }
    }
}
