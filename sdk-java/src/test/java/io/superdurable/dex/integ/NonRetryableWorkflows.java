/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Sustainable Use License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.RecoveryErrorInfo;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import io.superdurable.dex.WaitForFailurePolicy;
import io.superdurable.dex.exceptions.NonRetryableException;

import java.time.Duration;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

final class NonRetryableWorkflows {
    static final String DETAIL = "java card rejected";
    /** Handler calls per Flow ID; the Worker runs in the test JVM. */
    static final Map<String, Integer> ATTEMPTS_BY_FLOW_ID = new ConcurrentHashMap<>();

    private NonRetryableWorkflows() {
    }

    static final class CardRejectedException extends RuntimeException {
        private static final long serialVersionUID = 1L;

        CardRejectedException() {
            super(DETAIL);
        }
    }

    /** A real business retry policy: unlimited attempts an hour apart. */
    private static RetryPolicy hourlyRetry() {
        return RetryPolicy.newBuilder()
                .initialInterval(Duration.ofHours(1))
                .maximumInterval(Duration.ofHours(1))
                .build();
    }

    private static NonRetryableException failPermanently(final Context context) {
        ATTEMPTS_BY_FLOW_ID.merge(context.getFlowId(), 1, Integer::sum);
        return new NonRetryableException(new CardRejectedException());
    }

    private static StepDecision completeWithRecovery(final Context context) {
        final RecoveryErrorInfo recovery = context.getRecoveryError();
        if (recovery == null) {
            throw new NonRetryableException("recovery error is missing");
        }
        return StepDecision.gracefulComplete(recovery.getErrorType() + ": " + recovery.getDetail());
    }

    static final class ExecuteWorkflow implements Flow<String> {
        private final ChargeStep start = new ChargeStep();
        private final RecoveryStep recover = new RecoveryStep();

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(start).otherSteps(recover);
        }
    }

    static final class ChargeStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            throw failPermanently(context);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(hourlyRetry())
                    .onExecuteFailureProceedTo(RecoveryStep.class)
                    .build();
        }
    }

    static final class RecoveryStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return completeWithRecovery(context);
        }
    }

    static final class WaitForWorkflow implements Flow<String> {
        private final WaitForStep start = new WaitForStep();

        @Override
        public StepList<String> getSteps() {
            return StepList.startStep(start);
        }
    }

    static final class WaitForStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String input) {
            throw failPermanently(context);
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return completeWithRecovery(context);
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .waitForRetry(hourlyRetry())
                    .waitForFailure(WaitForFailurePolicy.PROCEED)
                    .build();
        }
    }
}
