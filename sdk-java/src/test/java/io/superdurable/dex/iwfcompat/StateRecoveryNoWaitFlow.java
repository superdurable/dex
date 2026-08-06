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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDef;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepOptions;

import java.util.Arrays;
import java.util.List;

final class StateRecoveryNoWaitFlow implements Flow<Integer> {
    private final RecoverNoWaitStep recover = new RecoverNoWaitStep();
    private final FailingNoWaitStep start = new FailingNoWaitStep();

    @Override
    public List<StepDef> getSteps() {
        return Arrays.asList(
                StepDef.startStep(start),
                StepDef.nonStartStep(recover));
    }

    final class FailingNoWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            throw new IllegalStateException("execute failure");
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .onExecuteFailureProceedTo(recover)
                    .build();
        }
    }

    static final class RecoverNoWaitStep implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer input) {
            return StepDecision.gracefulComplete(input * 2);
        }
    }
}
