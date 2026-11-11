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

import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMap;
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

import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;

/** Takes the first of three bids, cancels the sibling branches, and drains the late bid. */
class JoinIntegCancellingWorkflow implements Flow<JoinIntegCancellingWorkflow.CarrierList> {
    static final String BRANCH_STEP_TYPE = "JoinIntegCancellingBranch";

    final Join<String> bidReceived = Join.define("bidReceived", String.class);
    final ChannelMap<String> carrierReleased = ChannelMap.define("carrierReleased", String.class);
    final Channel<String> joinGate = Channel.define("joinGate", String.class);
    private final JoinIntegCancellingSplit split = new JoinIntegCancellingSplit();
    private final JoinIntegCancellingBranch branch = new JoinIntegCancellingBranch();
    private final JoinIntegCancellingJoin join = new JoinIntegCancellingJoin();
    private final JoinIntegCancellingDrain drain = new JoinIntegCancellingDrain();

    @Override
    public StepList<CarrierList> getSteps() {
        return StepList.startStep(split).otherSteps(branch, join, drain);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(bidReceived, carrierReleased, joinGate);
    }

    @RPC
    public void releaseCarrier(final Context context, final String carrier) {
        carrierReleased.publish(context, carrier, carrier);
    }

    @RPC
    public void releaseJoinGate(final Context context) {
        joinGate.publish(context, "open");
    }

    final class JoinIntegCancellingSplit implements Step<CarrierList> {
        @Override
        public Class<CarrierList> getInputType() {
            return CarrierList.class;
        }

        @Override
        public StepDecision execute(final Context context, final CarrierList input) {
            final List<Function<JoinRound, StepMovement<?>>> branchBuilders =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            for (String carrier : input.carriers) {
                branchBuilders.add(round -> StepMovement.of(
                        JoinIntegCancellingBranch.class,
                        new CarrierInput(carrier, round)));
            }
            final List<StepMovement<?>> movements = bidReceived.fork(
                    context,
                    branchBuilders,
                    round -> StepMovement.of(JoinIntegCancellingJoin.class, round));
            return StepDecision.goToMany(movements.toArray(new StepMovement<?>[0]));
        }
    }

    final class JoinIntegCancellingBranch implements Step<CarrierInput> {
        @Override
        public Class<CarrierInput> getInputType() {
            return CarrierInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final CarrierInput input) {
            return Wait.until(carrierReleased.forOne(input.carrier));
        }

        @Override
        public StepDecision execute(final Context context, final CarrierInput input) {
            return bidReceived.endBranch(context, input.round, input.carrier);
        }
    }

    final class JoinIntegCancellingJoin implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.allOf(joinGate.forOne(), bidReceived.forFirstBranch(round));
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            final BidOutcome outcome = new BidOutcome();
            outcome.round = round;
            outcome.winningBids = new ArrayList<String>(bidReceived.getBranchResults(context, round));
            return StepDecision.goTo(JoinIntegCancellingDrain.class, outcome)
                    .withCancelingSiblingSteps(JoinIntegCancellingBranch.class);
        }
    }

    final class JoinIntegCancellingDrain implements Step<BidOutcome> {
        @Override
        public Class<BidOutcome> getInputType() {
            return BidOutcome.class;
        }

        @Override
        public Wait waitFor(final Context context, final BidOutcome outcome) {
            // One bid was consumed by the join and one branch was canceled; one late bid remains.
            return Wait.until(bidReceived.forRemainingBranches(
                    outcome.round,
                    outcome.round.getBranchCount() - 1));
        }

        @Override
        public StepDecision execute(final Context context, final BidOutcome outcome) {
            outcome.lateBids = new ArrayList<String>(
                    bidReceived.getRemainingBranchResults(context, outcome.round));
            return StepDecision.gracefulComplete(outcome);
        }
    }

    public static final class CarrierList {
        public List<String> carriers;

        public CarrierList() {
        }

        CarrierList(final List<String> carriers) {
            this.carriers = carriers;
        }
    }

    public static final class CarrierInput {
        public String carrier;
        public JoinRound round;

        public CarrierInput() {
        }

        CarrierInput(final String carrier, final JoinRound round) {
            this.carrier = carrier;
            this.round = round;
        }
    }

    public static final class BidOutcome {
        public JoinRound round;
        public List<String> winningBids;
        public List<String> lateBids;
    }
}
