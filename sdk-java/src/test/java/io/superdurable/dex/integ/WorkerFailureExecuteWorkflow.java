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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepDurability;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;

import java.time.Duration;
import java.util.concurrent.atomic.AtomicInteger;

final class WorkerFailureExecuteWorkflow implements Flow<Void> {
    private final RetryingExecuteStep start = new RetryingExecuteStep();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(start);
    }

    static final class RetryingExecuteStep implements Step<Void> {
        private final AtomicInteger attempts = new AtomicInteger();

        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (attempts.incrementAndGet() == 1) {
                throw new IllegalStateException("Java execute retry failure");
            }
            return StepDecision.gracefulComplete();
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(retryPolicy())
                    .executeDurability(StepDurability.SYNC)
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
