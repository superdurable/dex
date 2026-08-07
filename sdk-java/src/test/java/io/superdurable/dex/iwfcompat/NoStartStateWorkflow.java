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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;

final class NoStartStateWorkflow implements Flow<Void> {
    static final long RPC_OUTPUT = 100L;
    private final TriggeredStep triggered = new TriggeredStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.withoutStartStep(triggered);
    }

    @RPC
    public RPCResult<Long> invoke(final Context context, final String input) {
        if (context.getFlowId().isEmpty() || context.getRunId().isEmpty()) {
            throw new IllegalStateException("invalid RPC context");
        }
        return RPCResult.of(RPC_OUTPUT, StepMovement.of(triggered, null));
    }

    static final class TriggeredStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(1);
        }
    }
}
