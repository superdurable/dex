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
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;

class NoStartStateDeadEndWorkflow implements Flow<Void> {
    static final long RPC_OUTPUT = 100L;
    final Channel<Void> idleSignal = Channel.define("idle-signal", Void.class);
    final Channel<Void> idleInternal = Channel.define("idle-internal", Void.class);
    private final NoStartStateDeadEndStep start = new NoStartStateDeadEndStep();
    private final NoStartStateCompleteStep complete = new NoStartStateCompleteStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(start).otherSteps(complete);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(idleSignal, idleInternal);
    }

    @RPC
    public RPCResult<Integer> signalSize(final Context context) {
        return RPCResult.of(idleSignal.size(context));
    }

    @RPC
    public RPCResult<Integer> publishInternal(final Context context) {
        idleInternal.publish(context, null);
        return RPCResult.of(idleInternal.size(context));
    }

    @RPC
    public RPCResult<Long> invoke(final Context context, final String input) {
        if (context.getFlowId().isEmpty() || context.getRunId().isEmpty()) {
            throw new IllegalStateException("invalid RPC context");
        }
        return RPCResult.of(
                RPC_OUTPUT, StepMovement.of(NoStartStateCompleteStep.class, null));
    }
}

final class NoStartStateDeadEndStep implements Step<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StepDecision execute(final Context context, final Void input) {
        return StepDecision.deadEnd();
    }
}

final class NoStartStateCompleteStep implements Step<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StepDecision execute(final Context context, final Void input) {
        return StepDecision.gracefulComplete();
    }
}
