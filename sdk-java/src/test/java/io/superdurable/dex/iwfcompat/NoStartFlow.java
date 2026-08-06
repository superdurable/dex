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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepMovement;

import java.util.Collections;
import java.util.List;

final class NoStartFlow implements Flow<Void> {
    private final TriggeredStep triggered = new TriggeredStep();

    @Override
    public List<StepDef> getSteps() {
        return Collections.singletonList(StepDef.nonStartStep(triggered));
    }

    @RPC
    public RPCResult<Long> invoke(final Context context, final String input) {
        return RPCResult.of(1L, StepMovement.of(triggered, null));
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
