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

package io.superdurable.dex.patterns.workflowpatterns;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/**
 * WCP-33 Generalised AND-Join: the join fires once per invoice and payment pair; extra messages
 * stay queued for the next firing.
 */
@Component
public class GeneralizedAndJoinFlow implements Flow<GeneralizedAndJoinFlow.MatchProgress> {
    public static final Channel<String> invoiceReceived =
            Channel.define("invoiceReceived", String.class);
    public static final Channel<String> paymentReceived =
            Channel.define("paymentReceived", String.class);

    /** How many matches the Flow expects and how many it has made. */
    public record MatchProgress(int expectedMatches, int matchedCount) {
    }

    /** One invoice matched with one payment. */
    public record InvoicePaymentMatch(String invoiceId, String paymentId) {
    }

    private final MatchInvoiceAndPayment matchInvoiceAndPayment = new MatchInvoiceAndPayment();
    private final RecordMatch recordMatch = new RecordMatch();

    @Override
    public StepList<MatchProgress> getSteps() {
        return StepList.startStep(matchInvoiceAndPayment).otherSteps(recordMatch);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(invoiceReceived, paymentReceived);
    }

    @RPC
    public void deliverInvoice(final Context context, final String invoiceId) {
        invoiceReceived.publish(context, invoiceId);
    }

    @RPC
    public void deliverPayment(final Context context, final String paymentId) {
        paymentReceived.publish(context, paymentId);
    }

    static final class MatchInvoiceAndPayment implements Step<MatchProgress> {
        @Override
        public Class<MatchProgress> getInputType() {
            return MatchProgress.class;
        }

        @Override
        public Wait waitFor(final Context context, final MatchProgress progress) {
            return Wait.allOf(invoiceReceived.forOne(), paymentReceived.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final MatchProgress progress) {
            final InvoicePaymentMatch match = new InvoicePaymentMatch(
                    invoiceReceived.getConditionResults(context).get(0),
                    paymentReceived.getConditionResults(context).get(0));
            final int matchedCount = progress.matchedCount() + 1;
            if (matchedCount < progress.expectedMatches()) {
                return StepDecision.goToMany(
                        StepMovement.of(RecordMatch.class, match),
                        StepMovement.of(
                                MatchInvoiceAndPayment.class,
                                new MatchProgress(progress.expectedMatches(), matchedCount)));
            }
            return StepDecision.goTo(RecordMatch.class, match);
        }
    }

    static final class RecordMatch implements Step<InvoicePaymentMatch> {
        @Override
        public Class<InvoicePaymentMatch> getInputType() {
            return InvoicePaymentMatch.class;
        }

        @Override
        public StepDecision execute(final Context context, final InvoicePaymentMatch match) {
            return StepDecision.gracefulComplete(match);
        }
    }
}
