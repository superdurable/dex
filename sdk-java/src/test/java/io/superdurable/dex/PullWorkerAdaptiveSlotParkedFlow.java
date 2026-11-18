/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex;

import java.time.Duration;

// Stays running without a slot, so RPCs can target it.
public class PullWorkerAdaptiveSlotParkedFlow implements Flow<Void> {
    private final PullWorkerAdaptiveSlotHandlers handlers;

    // Client RPC stubs need a public no-argument constructor.
    public PullWorkerAdaptiveSlotParkedFlow() {
        this(new PullWorkerAdaptiveSlotHandlers());
    }

    PullWorkerAdaptiveSlotParkedFlow(final PullWorkerAdaptiveSlotHandlers handlers) {
        this.handlers = handlers;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(new PullWorkerAdaptiveSlotParkedStep());
    }

    @RPC
    public RPCResult<String> blockUntilReleased(final Context context) {
        return RPCResult.of(handlers.runBlockingRpc(context));
    }

    @RPC
    public RPCResult<String> getSlotStatus(final Context context) {
        return RPCResult.of("ok");
    }

    static final class PullWorkerAdaptiveSlotParkedStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(Timer.byDuration(Duration.ofHours(1), "parked"));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete("parked");
        }
    }
}
