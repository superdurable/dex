/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package io.superdurable.dex.patterns.failfast;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.patterns.failfast.FakePaymentProvider.PayoutRejectedException;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class ExpenseReimbursementFlow implements Flow<ExpenseClaim> {
    public record ClaimRejection(String reason, int submitAttempt) {
    }

    public record PayoutOutcome(String status, String detail, int submitAttempt) {
    }

    public static final Attribute<PayoutOutcome> payoutOutcome =
            Attribute.define("PayoutOutcome", PayoutOutcome.class);

    private final FakePaymentProvider provider;
    private final SubmitPayout submitPayout = new SubmitPayout();
    private final RecordRejectedClaim recordRejectedClaim = new RecordRejectedClaim();

    public ExpenseReimbursementFlow(final FakePaymentProvider provider) {
        this.provider = provider;
    }

    @Override
    public StepList<ExpenseClaim> getSteps() {
        return StepList.startStep(submitPayout).otherSteps(recordRejectedClaim);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(payoutOutcome);
    }

    // dex:group group-id:payout group-label:"Payout"
    // dex:explanation text:"Submit the payout; retry a timeout and route a rejection at once."
    final class SubmitPayout implements Step<ExpenseClaim> {
        @Override
        public Class<ExpenseClaim> getInputType() {
            return ExpenseClaim.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .initialInterval(Duration.ofSeconds(1))
                            .backoffCoefficient(2.0)
                            .maximumInterval(Duration.ofMinutes(5))
                            .totalDuration(Duration.ofHours(1))
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final ExpenseClaim claim) {
            final String payoutId;
            try {
                payoutId = provider.submitPayout(context.getFlowId(), claim);
            } catch (final PayoutRejectedException rejection) {
                return StepDecision.goTo(RecordRejectedClaim.class,
                        new ClaimRejection(rejection.getMessage(), context.getAttempt()));
            }
            payoutOutcome.set(context, new PayoutOutcome("PAID", payoutId, context.getAttempt()));
            return StepDecision.gracefulComplete(payoutId);
        }
    }

    // dex:group group-id:rejection group-label:"Rejection"
    // dex:explanation text:"Record the rejected claim and its reason."
    final class RecordRejectedClaim implements Step<ClaimRejection> {
        @Override
        public Class<ClaimRejection> getInputType() {
            return ClaimRejection.class;
        }

        @Override
        public StepDecision execute(final Context context, final ClaimRejection rejection) {
            payoutOutcome.set(context,
                    new PayoutOutcome("REJECTED", rejection.reason(), rejection.submitAttempt()));
            return StepDecision.gracefulComplete("rejected: " + rejection.reason());
        }
    }
}
