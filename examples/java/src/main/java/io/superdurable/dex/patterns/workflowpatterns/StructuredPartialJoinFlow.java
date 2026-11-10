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
import io.superdurable.dex.ChannelMap;
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

/** WCP-9 and WCP-30: the join fires after requiredQuotes quotes; a reset Step re-enables it. */
@Component
public class StructuredPartialJoinFlow implements Flow<StructuredPartialJoinFlow.QuoteRound> {
    public static final Channel<String> supplierAReleased =
            Channel.define("supplierAReleased", String.class);
    public static final Channel<String> supplierBReleased =
            Channel.define("supplierBReleased", String.class);
    public static final Channel<String> supplierCReleased =
            Channel.define("supplierCReleased", String.class);
    public static final ChannelMap<String> quoteReceived =
            ChannelMap.define("quoteReceived", String.class);
    private static final int SUPPLIER_COUNT = 3;

    /** One join round: the quotes it needs, its number, and how many rounds run. */
    public record QuoteRound(int requiredQuotes, int round, int roundCount) {
    }

    /** The quotes that fired the join in one round. */
    public record AcceptedQuotes(int round, List<String> acceptedQuotes) {
    }

    /** The quotes the reset Step absorbed in the last round. */
    public record IgnoredQuotes(int round, List<String> ignoredQuotes) {
    }

    private final RequestQuotes requestQuotes = new RequestQuotes();
    private final QuoteFromSupplierA quoteFromSupplierA = new QuoteFromSupplierA();
    private final QuoteFromSupplierB quoteFromSupplierB = new QuoteFromSupplierB();
    private final QuoteFromSupplierC quoteFromSupplierC = new QuoteFromSupplierC();
    private final AcceptQuotes acceptQuotes = new AcceptQuotes();
    private final PlaceOrder placeOrder = new PlaceOrder();
    private final ResetQuoteJoin resetQuoteJoin = new ResetQuoteJoin();

    @Override
    public StepList<QuoteRound> getSteps() {
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

    static final class RequestQuotes implements Step<QuoteRound> {
        @Override
        public Class<QuoteRound> getInputType() {
            return QuoteRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final QuoteRound quoteRound) {
            return StepDecision.goToMany(
                    StepMovement.of(QuoteFromSupplierA.class, quoteRound.round()),
                    StepMovement.of(QuoteFromSupplierB.class, quoteRound.round()),
                    StepMovement.of(QuoteFromSupplierC.class, quoteRound.round()),
                    StepMovement.of(AcceptQuotes.class, quoteRound));
        }
    }

    static final class QuoteFromSupplierA implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer round) {
            return Wait.until(supplierAReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer round) {
            quoteReceived.publish(context, JoinRoundInstance.of(round), "A");
            return StepDecision.deadEnd();
        }
    }

    static final class QuoteFromSupplierB implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer round) {
            return Wait.until(supplierBReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer round) {
            quoteReceived.publish(context, JoinRoundInstance.of(round), "B");
            return StepDecision.deadEnd();
        }
    }

    static final class QuoteFromSupplierC implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer round) {
            return Wait.until(supplierCReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer round) {
            quoteReceived.publish(context, JoinRoundInstance.of(round), "C");
            return StepDecision.deadEnd();
        }
    }

    static final class AcceptQuotes implements Step<QuoteRound> {
        @Override
        public Class<QuoteRound> getInputType() {
            return QuoteRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final QuoteRound quoteRound) {
            return Wait.until(quoteReceived.forN(
                    JoinRoundInstance.of(quoteRound.round()), quoteRound.requiredQuotes()));
        }

        @Override
        public StepDecision execute(final Context context, final QuoteRound quoteRound) {
            final List<String> accepted = quoteReceived
                    .getConditionResults(context, JoinRoundInstance.of(quoteRound.round()))
                    .stream()
                    .sorted()
                    .toList();
            return StepDecision.goToMany(
                    StepMovement.of(
                            PlaceOrder.class, new AcceptedQuotes(quoteRound.round(), accepted)),
                    StepMovement.of(ResetQuoteJoin.class, quoteRound));
        }
    }

    static final class PlaceOrder implements Step<AcceptedQuotes> {
        @Override
        public Class<AcceptedQuotes> getInputType() {
            return AcceptedQuotes.class;
        }

        @Override
        public StepDecision execute(final Context context, final AcceptedQuotes acceptedQuotes) {
            return StepDecision.gracefulComplete(acceptedQuotes);
        }
    }

    static final class ResetQuoteJoin implements Step<QuoteRound> {
        @Override
        public Class<QuoteRound> getInputType() {
            return QuoteRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final QuoteRound quoteRound) {
            return Wait.until(quoteReceived.forN(
                    JoinRoundInstance.of(quoteRound.round()),
                    SUPPLIER_COUNT - quoteRound.requiredQuotes()));
        }

        @Override
        public StepDecision execute(final Context context, final QuoteRound quoteRound) {
            if (quoteRound.round() < quoteRound.roundCount()) {
                return StepDecision.goTo(
                        RequestQuotes.class,
                        new QuoteRound(
                                quoteRound.requiredQuotes(),
                                quoteRound.round() + 1,
                                quoteRound.roundCount()));
            }
            return StepDecision.gracefulComplete(new IgnoredQuotes(
                    quoteRound.round(),
                    quoteReceived
                            .getConditionResults(context, JoinRoundInstance.of(quoteRound.round()))
                            .stream()
                            .sorted()
                            .toList()));
        }
    }
}
