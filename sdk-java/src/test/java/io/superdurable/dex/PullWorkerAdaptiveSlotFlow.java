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

// Runs one Step whose input chooses how long it holds its slot.
public class PullWorkerAdaptiveSlotFlow implements Flow<String> {
    private final PullWorkerAdaptiveSlotHandlers handlers;

    // Client RPC stubs need a public no-argument constructor.
    public PullWorkerAdaptiveSlotFlow() {
        this(new PullWorkerAdaptiveSlotHandlers());
    }

    PullWorkerAdaptiveSlotFlow(final PullWorkerAdaptiveSlotHandlers handlers) {
        this.handlers = handlers;
    }

    PullWorkerAdaptiveSlotHandlers handlers() {
        return handlers;
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(new PullWorkerAdaptiveSlotStep(handlers));
    }

    @RPC
    public RPCResult<String> getSlotStatus(final Context context) {
        return RPCResult.of("ok");
    }

    static final class PullWorkerAdaptiveSlotStep implements Step<String> {
        private final PullWorkerAdaptiveSlotHandlers handlers;

        PullWorkerAdaptiveSlotStep(final PullWorkerAdaptiveSlotHandlers handlers) {
            this.handlers = handlers;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String mode) {
            handlers.runStep(context, mode);
            return StepDecision.gracefulComplete(mode);
        }
    }
}
