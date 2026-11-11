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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.List;

/** WCP-9 and WCP-30: the join fires after requiredQuotes quotes; the next split resets it. */
@Component
public class StructuredPartialJoinFlow implements Flow<StructuredPartialJoinFlow.QuoteRound> {
    public static final Channel<String> supplierAReleased =
            Channel.define("supplierAReleased", String.class);
    public static final Channel<String> supplierBReleased =
            Channel.define("supplierBReleased", String.class);
    public static final Channel<String> supplierCReleased =
            Channel.define("supplierCReleased", String.class);
    public static final Join<String> quoteReceived = Join.define("quoteReceived", String.class);
    public static final Attribute<IgnoredQuotes> ignoredQuotes =
            Attribute.define("ignoredQuotes", IgnoredQuotes.class);

    /**
     * One join round: the quotes it needs, its number, how many rounds run, and the previous
     * Join round, which is null in the first round.
     */
    public record QuoteRound(
            int requiredQuotes, int round, int roundCount, JoinRound previousRound) {
    }

    /** The join Step input: the quote round and the Join round it waits on. */
    public record QuoteJoin(QuoteRound quoteRound, JoinRound joinRound) {
    }

    /** The quotes that fired the join in one round. */
    public record AcceptedQuotes(int round, List<String> acceptedQuotes) {
    }

    /** The late quotes of one round, consumed when the next round reset the join. */
    public record IgnoredQuotes(int round, List<String> ignoredQuotes) {
    }

    private final RequestQuotes requestQuotes = new RequestQuotes();
    private final QuoteFromSupplierA quoteFromSupplierA = new QuoteFromSupplierA();
    private final QuoteFromSupplierB quoteFromSupplierB = new QuoteFromSupplierB();
    private final QuoteFromSupplierC quoteFromSupplierC = new QuoteFromSupplierC();
    private final AcceptQuotes acceptQuotes = new AcceptQuotes();
    private final PlaceOrder placeOrder = new PlaceOrder();

    @Override
    public StepList<QuoteRound> getSteps() {
        return StepList.startStep(requestQuotes).otherSteps(
                quoteFromSupplierA,
                quoteFromSupplierB,
                quoteFromSupplierC,
                acceptQuotes,
                placeOrder);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                supplierAReleased,
                supplierBReleased,
                supplierCReleased,
                quoteReceived,
                ignoredQuotes);
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
        public Wait waitFor(final Context context, final QuoteRound quoteRound) {
            if (quoteRound.previousRound() == null) {
                return Wait.skipImmediately();
            }
            return Wait.until(quoteReceived.forRemainingBranches(
                    quoteRound.previousRound(), quoteRound.requiredQuotes()));
        }

        @Override
        public StepDecision execute(final Context context, final QuoteRound quoteRound) {
            if (quoteRound.previousRound() != null) {
                ignoredQuotes.set(context, new IgnoredQuotes(
                        quoteRound.round() - 1,
                        quoteReceived
                                .getRemainingBranchResults(context, quoteRound.previousRound())
                                .stream()
                                .sorted()
                                .toList()));
            }
            return StepDecision.goToMany(quoteReceived.fork(
                    context,
                    List.of(
                            round -> StepMovement.of(QuoteFromSupplierA.class, round),
                            round -> StepMovement.of(QuoteFromSupplierB.class, round),
                            round -> StepMovement.of(QuoteFromSupplierC.class, round)),
                    round -> StepMovement.of(AcceptQuotes.class, new QuoteJoin(quoteRound, round))));
        }
    }

    static final class QuoteFromSupplierA implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(supplierAReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return quoteReceived.endBranch(context, round, "A");
        }
    }

    static final class QuoteFromSupplierB implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(supplierBReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return quoteReceived.endBranch(context, round, "B");
        }
    }

    static final class QuoteFromSupplierC implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(supplierCReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return quoteReceived.endBranch(context, round, "C");
        }
    }

    static final class AcceptQuotes implements Step<QuoteJoin> {
        @Override
        public Class<QuoteJoin> getInputType() {
            return QuoteJoin.class;
        }

        @Override
        public Wait waitFor(final Context context, final QuoteJoin quoteJoin) {
            return Wait.until(quoteReceived.forBranches(
                    quoteJoin.joinRound(), quoteJoin.quoteRound().requiredQuotes()));
        }

        @Override
        public StepDecision execute(final Context context, final QuoteJoin quoteJoin) {
            final QuoteRound quoteRound = quoteJoin.quoteRound();
            final List<String> accepted = quoteReceived
                    .getBranchResults(context, quoteJoin.joinRound())
                    .stream()
                    .sorted()
                    .toList();
            final StepMovement<AcceptedQuotes> placeOrder = StepMovement.of(
                    PlaceOrder.class, new AcceptedQuotes(quoteRound.round(), accepted));
            if (quoteRound.round() >= quoteRound.roundCount()) {
                // The last round's late quotes stay pending until the Flow closes.
                return StepDecision.goToMany(placeOrder);
            }
            return StepDecision.goToMany(
                    placeOrder,
                    StepMovement.of(
                            RequestQuotes.class,
                            new QuoteRound(
                                    quoteRound.requiredQuotes(),
                                    quoteRound.round() + 1,
                                    quoteRound.roundCount(),
                                    quoteJoin.joinRound())));
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
}
