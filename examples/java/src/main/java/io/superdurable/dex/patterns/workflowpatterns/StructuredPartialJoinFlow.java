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

import java.util.List;

/** WCP-9 and WCP-30: the join fires after requiredQuotes quotes; a reset Step absorbs the rest. */
@Component
public class StructuredPartialJoinFlow implements Flow<Integer> {
    public static final Channel<String> supplierAReleased =
            Channel.define("supplierAReleased", String.class);
    public static final Channel<String> supplierBReleased =
            Channel.define("supplierBReleased", String.class);
    public static final Channel<String> supplierCReleased =
            Channel.define("supplierCReleased", String.class);
    public static final Channel<String> quoteReceived =
            Channel.define("quoteReceived", String.class);
    private static final int SUPPLIER_COUNT = 3;

    private final RequestQuotes requestQuotes = new RequestQuotes();
    private final QuoteFromSupplierA quoteFromSupplierA = new QuoteFromSupplierA();
    private final QuoteFromSupplierB quoteFromSupplierB = new QuoteFromSupplierB();
    private final QuoteFromSupplierC quoteFromSupplierC = new QuoteFromSupplierC();
    private final AcceptQuotes acceptQuotes = new AcceptQuotes();
    private final PlaceOrder placeOrder = new PlaceOrder();
    private final ResetQuoteJoin resetQuoteJoin = new ResetQuoteJoin();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(requestQuotes).otherSteps(
                quoteFromSupplierA,
                quoteFromSupplierB,
                quoteFromSupplierC,
                acceptQuotes,
                placeOrder,
                resetQuoteJoin);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                supplierAReleased, supplierBReleased, supplierCReleased, quoteReceived);
    }

    /** Releases the quote of supplier "A", "B", or "C". */
    @RPC
    public void releaseSupplierQuote(final Context context, final String supplier) {
        supplierReleasedChannel(supplier).publish(context, supplier);
    }

    private static Channel<String> supplierReleasedChannel(final String supplier) {
        if ("A".equals(supplier)) {
            return supplierAReleased;
        }
        if ("B".equals(supplier)) {
            return supplierBReleased;
        }
        if ("C".equals(supplier)) {
            return supplierCReleased;
        }
        throw new IllegalArgumentException("unknown supplier: " + supplier);
    }

    static final class RequestQuotes implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer requiredQuotes) {
            return StepDecision.goToMany(
                    StepMovement.of(QuoteFromSupplierA.class, null),
                    StepMovement.of(QuoteFromSupplierB.class, null),
                    StepMovement.of(QuoteFromSupplierC.class, null),
                    StepMovement.of(AcceptQuotes.class, requiredQuotes));
        }
    }

    static final class QuoteFromSupplierA implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(supplierAReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            quoteReceived.publish(context, "A");
            return StepDecision.deadEnd();
        }
    }

    static final class QuoteFromSupplierB implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(supplierBReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            quoteReceived.publish(context, "B");
            return StepDecision.deadEnd();
        }
    }

    static final class QuoteFromSupplierC implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(supplierCReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            quoteReceived.publish(context, "C");
            return StepDecision.deadEnd();
        }
    }

    static final class AcceptQuotes implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer requiredQuotes) {
            return Wait.until(quoteReceived.forN(requiredQuotes));
        }

        @Override
        public StepDecision execute(final Context context, final Integer requiredQuotes) {
            final List<String> accepted = quoteReceived.getConditionResults(context);
            return StepDecision.goToMany(
                    StepMovement.of(PlaceOrder.class, accepted.toArray(new String[0])),
                    StepMovement.of(ResetQuoteJoin.class, SUPPLIER_COUNT - requiredQuotes));
        }
    }

    static final class PlaceOrder implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] accepted) {
            return StepDecision.gracefulComplete(List.of(accepted).stream().sorted().toList());
        }
    }

    static final class ResetQuoteJoin implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer remainingQuotes) {
            return Wait.until(quoteReceived.forN(remainingQuotes));
        }

        @Override
        public StepDecision execute(final Context context, final Integer remainingQuotes) {
            return StepDecision.gracefulComplete(
                    quoteReceived.getConditionResults(context).stream().sorted().toList());
        }
    }
}
