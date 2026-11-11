/*
 * Portions of this file are derived from indeedeng/iwf-java-sdk.
 * Those portions are licensed under the Apache License, Version 2.0.
 * See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
 *
 * Modifications Copyright (c) 2026 Super Durable, Inc.
 *
 * Modifications are licensed under the Sustainable Use License 1.0.
 * Third-Party Materials remain under the Apache License, Version 2.0.
 * See LICENSE and LEGACY_NOTICES.md.
 */

package io.superdurable.dex.integ;

import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.function.Function;

/** Accepts two of three quotes per round and resets each round before forking the next one. */
class JoinIntegPartialWorkflow implements Flow<JoinIntegPartialWorkflow.QuoteRequest> {
    static final String ACCEPT_STEP_TYPE = "JoinIntegPartialAccept";
    static final int REQUIRED_QUOTES = 2;
    static final int ROUND_COUNT = 2;
    static final List<String> SUPPLIERS = Arrays.asList("A", "B", "C");

    final Join<String> quoteReceived = Join.define("quoteReceived", String.class);
    final ChannelMap<String> supplierReleased =
            ChannelMap.define("supplierReleased", String.class);
    private final JoinIntegPartialRequest request = new JoinIntegPartialRequest();
    private final JoinIntegPartialQuote quote = new JoinIntegPartialQuote();
    private final JoinIntegPartialAccept accept = new JoinIntegPartialAccept();

    static String releaseInstance(final String supplier, final int roundNumber) {
        return supplier + "-" + roundNumber;
    }

    @Override
    public StepList<QuoteRequest> getSteps() {
        return StepList.startStep(request).otherSteps(quote, accept);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(quoteReceived, supplierReleased);
    }

    @RPC
    public void releaseSupplier(final Context context, final String instance) {
        supplierReleased.publish(context, instance, instance);
    }

    @RPC
    public RPCResult<Integer> getSupplierReleaseSize(final Context context, final String instance) {
        return RPCResult.of(supplierReleased.size(context, instance));
    }

    final class JoinIntegPartialRequest implements Step<QuoteRequest> {
        @Override
        public Class<QuoteRequest> getInputType() {
            return QuoteRequest.class;
        }

        @Override
        public Wait waitFor(final Context context, final QuoteRequest input) {
            if (input.previousRound == null) {
                return Wait.skipImmediately();
            }
            return Wait.until(quoteReceived.forRemainingBranches(
                    input.previousRound,
                    REQUIRED_QUOTES));
        }

        @Override
        public StepDecision execute(final Context context, final QuoteRequest input) {
            if (input.previousRound != null) {
                input.outcome.remainingQuotes.add(new ArrayList<String>(
                        quoteReceived.getRemainingBranchResults(context, input.previousRound)));
            }
            final List<Function<JoinRound, StepMovement<?>>> branchBuilders =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            for (String supplier : SUPPLIERS) {
                branchBuilders.add(round -> StepMovement.of(
                        JoinIntegPartialQuote.class,
                        new SupplierInput(supplier, input.roundNumber, round)));
            }
            final List<StepMovement<?>> movements = quoteReceived.fork(
                    context,
                    branchBuilders,
                    round -> StepMovement.of(
                            JoinIntegPartialAccept.class,
                            new AcceptInput(input, round)));
            return StepDecision.goToMany(movements.toArray(new StepMovement<?>[0]));
        }
    }

    final class JoinIntegPartialQuote implements Step<SupplierInput> {
        @Override
        public Class<SupplierInput> getInputType() {
            return SupplierInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final SupplierInput input) {
            return Wait.until(supplierReleased.forOne(
                    releaseInstance(input.supplier, input.roundNumber)));
        }

        @Override
        public StepDecision execute(final Context context, final SupplierInput input) {
            return quoteReceived.endBranch(context, input.round, input.supplier);
        }
    }

    final class JoinIntegPartialAccept implements Step<AcceptInput> {
        @Override
        public Class<AcceptInput> getInputType() {
            return AcceptInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final AcceptInput input) {
            return Wait.until(quoteReceived.forBranches(input.round, REQUIRED_QUOTES));
        }

        @Override
        public StepDecision execute(final Context context, final AcceptInput input) {
            final QuoteRequest request = input.request;
            request.outcome.roundKeys.add(input.round.getRoundKey());
            request.outcome.acceptedQuotes.add(new ArrayList<String>(
                    quoteReceived.getBranchResults(context, input.round)));
            if (request.roundNumber == ROUND_COUNT) {
                return StepDecision.forceComplete(request.outcome);
            }
            final QuoteRequest next = new QuoteRequest();
            next.roundNumber = request.roundNumber + 1;
            next.previousRound = input.round;
            next.outcome = request.outcome;
            return StepDecision.goTo(JoinIntegPartialRequest.class, next);
        }
    }

    public static final class QuoteRequest {
        public int roundNumber = 1;
        public JoinRound previousRound;
        public QuoteOutcome outcome = new QuoteOutcome();
    }

    public static final class SupplierInput {
        public String supplier;
        public int roundNumber;
        public JoinRound round;

        public SupplierInput() {
        }

        SupplierInput(final String supplier, final int roundNumber, final JoinRound round) {
            this.supplier = supplier;
            this.roundNumber = roundNumber;
            this.round = round;
        }
    }

    public static final class AcceptInput {
        public QuoteRequest request;
        public JoinRound round;

        public AcceptInput() {
        }

        AcceptInput(final QuoteRequest request, final JoinRound round) {
            this.request = request;
            this.round = round;
        }
    }

    public static final class QuoteOutcome {
        public List<String> roundKeys = new ArrayList<String>();
        public List<List<String>> acceptedQuotes = new ArrayList<List<String>>();
        public List<List<String>> remainingQuotes = new ArrayList<List<String>>();
    }
}
