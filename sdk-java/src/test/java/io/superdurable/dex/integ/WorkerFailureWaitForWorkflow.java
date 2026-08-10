// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

package io.superdurable.dex.integ;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;

import java.time.Duration;
import java.util.concurrent.atomic.AtomicInteger;

final class WorkerFailureWaitForWorkflow implements Flow<Void> {
    private final RetryingWaitForStep start = new RetryingWaitForStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(start);
    }

    static final class RetryingWaitForStep implements Step<Void> {
        private final AtomicInteger attempts = new AtomicInteger();

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            if (attempts.incrementAndGet() == 1) {
                throw new IllegalStateException("Java waitFor retry failure");
            }
            return Wait.skipImmediately();
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete();
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .waitForRetry(retryPolicy())
                    .waitForDurability(StepDurability.SYNC)
                    .build();
        }
    }

    private static RetryPolicy retryPolicy() {
        return RetryPolicy.newBuilder()
                .initialInterval(Duration.ofSeconds(5))
                .backoffCoefficient(1.0)
                .maximumInterval(Duration.ofSeconds(5))
                .maximumAttempts(2)
                .build();
    }
}
