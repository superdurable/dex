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

class RpcWorkflow implements Flow<Integer> {
    static final long RPC_OUTPUT = 100L;
    static final String HARDCODED_VALUE = "random-string";
    final Channel<Void> internal = Channel.define("rpc-internal", Void.class);
    final Attribute<String> data = Attribute.define("rpc-data", String.class);
    final Attribute<String> keyword = Attribute.define(
            "CustomKeywordField",
            String.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD));
    final Attribute<Integer> integer = Attribute.define(
            "CustomIntField",
            Integer.class,
            new AttributeIndex(AttributeIndex.Type.INT));
    private final RpcFirstStep first = new RpcFirstStep();
    private final RpcOutputStep output = new RpcOutputStep();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(first).otherSteps(output);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(data, keyword, integer, internal);
    }

    @RPC
    public void publishWithoutAttributeAccess(final Context context) {
        requireContext(context);
        internal.publish(context, null);
    }

    @RPC
    public RPCResult<Long> functionOne(final Context context, final String input) {
        requireContext(context);
        data.set(context, null);
        data.set(context, input);
        keyword.set(context, input);
        integer.set(context, Math.toIntExact(RPC_OUTPUT));
        internal.publish(context, null);
        return RPCResult.of(RPC_OUTPUT);
    }

    @RPC
    public RPCResult<Long> functionZero(final Context context) {
        requireContext(context);
        data.set(context, HARDCODED_VALUE);
        keyword.set(context, HARDCODED_VALUE);
        integer.set(context, Math.toIntExact(RPC_OUTPUT));
        internal.publish(context, null);
        return RPCResult.of(RPC_OUTPUT);
    }

    @RPC
    public void procedureOne(final Context context, final String input) {
        requireContext(context);
        data.set(context, input);
        keyword.set(context, input);
        integer.set(context, Math.toIntExact(RPC_OUTPUT));
        internal.publish(context, null);
    }

    @RPC
    public void procedureZero(final Context context) {
        requireContext(context);
        data.set(context, HARDCODED_VALUE);
        keyword.set(context, HARDCODED_VALUE);
        integer.set(context, Math.toIntExact(RPC_OUTPUT));
        internal.publish(context, null);
    }

    @RPC
    public RPCResult<Long> readOnly(final Context context, final String input) {
        requireContext(context);
        return RPCResult.of(RPC_OUTPUT);
    }

    @RPC
    public void setData(final Context context, final String input) {
        requireContext(context);
        data.set(context, input);
    }

    @RPC
    public RPCResult<String> getData(final Context context) {
        requireContext(context);
        return RPCResult.of(data.get(context));
    }

    @RPC
    public void setKeyword(final Context context, final String input) {
        requireContext(context);
        keyword.set(context, input);
    }

    @RPC
    public RPCResult<String> getKeyword(final Context context) {
        requireContext(context);
        return RPCResult.of(keyword.get(context));
    }

    private static void requireContext(final Context context) {
        if (context.getFlowId().isEmpty() || context.getRunId().isEmpty()) {
            throw new IllegalStateException("invalid RPC context");
        }
    }

    final class RpcFirstStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer input) {
            return Wait.until(internal.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.goTo(RpcOutputStep.class, 0);
        }
    }

    static final class RpcOutputStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(2);
        }
    }
}
