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
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.AttributeMap;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

/** WCP-36: RPCs add instances; the join re-evaluates on each arrival, then closes admission. */
@Component
public class DynamicPartialJoinForMultipleInstancesFlow
        implements Flow<DynamicPartialJoinForMultipleInstancesFlow.BidJoin> {
    private static final int ACCEPTED_BID_LIMIT = 1000;

    public static final Attribute<Boolean> isBidAdmissionClosed =
            Attribute.define("isBidAdmissionClosed", Boolean.class);
    public static final Channel<BidEvaluation> bidEvaluated =
            Channel.define("bidEvaluated", BidEvaluation.class);
    public static final AttributeMap<Boolean> evaluationStartedBids =
            AttributeMap.define("evaluationStartedBids", Boolean.class);
    public static final Channel<String> bidGateOpened = Channel.define("bidGateOpened", String.class);
    public static final Channel<String> contractSigned =
            Channel.define("contractSigned", String.class);

    /** The outcome of evaluating one bid. */
    public record BidEvaluation(int amount, boolean isAccepted) {
    }

    /** The join threshold and the bids accepted so far. */
    public record BidJoin(int requiredAcceptedBids, List<Integer> acceptedBids) {
    }

    private final JoinBids joinBids = new JoinBids();
    private final EvaluateBid evaluateBid = new EvaluateBid();
    private final AwardContract awardContract = new AwardContract();

    @Override
    public StepList<BidJoin> getSteps() {
        return StepList.startStep(joinBids).otherSteps(evaluateBid, awardContract);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                isBidAdmissionClosed,
                evaluationStartedBids,
                bidEvaluated,
                bidGateOpened,
                contractSigned);
    }

    /** Counts the EvaluateBid instances that are in flight at the gate. */
    @RPC(loadAttributeMaps = {"evaluationStartedBids"})
    public RPCResult<Integer> getStartedEvaluationCount(final Context context) {
        return RPCResult.of(evaluationStartedBids.getMapSize(context));
    }

    /** Lets count waiting EvaluateBid instances finish. */
    @RPC
    public void openBidGate(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            bidGateOpened.publish(context, "opened");
        }
    }

    @RPC
    public void signContract(final Context context) {
        contractSigned.publish(context, "signed");
    }

    /** Starts one more EvaluateBid instance, or returns false once admission is closed. */
    @RPC(lockAttributes = {"isBidAdmissionClosed"})
    public RPCResult<Boolean> addBid(final Context context, final Integer amount) {
        if (Boolean.TRUE.equals(isBidAdmissionClosed.get(context))) {
            return RPCResult.of(false);
        }
        return RPCResult.of(true, StepMovement.of(EvaluateBid.class, amount));
    }

    static final class JoinBids implements Step<BidJoin> {
        @Override
        public Class<BidJoin> getInputType() {
            return BidJoin.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLock(AttributeLock.of(isBidAdmissionClosed))
                    .build();
        }

        @Override
        public Wait waitFor(final Context context, final BidJoin join) {
            return Wait.until(bidEvaluated.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final BidJoin join) {
            final List<Integer> accepted = new ArrayList<Integer>(join.acceptedBids());
            for (final BidEvaluation evaluation : bidEvaluated.getConditionResults(context)) {
                if (evaluation.isAccepted()) {
                    accepted.add(evaluation.amount());
                }
            }
            if (accepted.size() >= join.requiredAcceptedBids()) {
                isBidAdmissionClosed.set(context, true);
                return StepDecision.goTo(
                        AwardContract.class, accepted.toArray(new Integer[0]));
            }
            return StepDecision.goTo(
                    JoinBids.class, new BidJoin(join.requiredAcceptedBids(), accepted));
        }
    }

    static final class EvaluateBid implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer amount) {
            evaluationStartedBids.set(context, Integer.toString(amount), true);
            return Wait.until(bidGateOpened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer amount) {
            bidEvaluated.publish(context, new BidEvaluation(amount, amount <= ACCEPTED_BID_LIMIT));
            return StepDecision.deadEnd();
        }
    }

    static final class AwardContract implements Step<Integer[]> {
        @Override
        public Class<Integer[]> getInputType() {
            return Integer[].class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer[] acceptedAmounts) {
            return Wait.until(contractSigned.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Integer[] acceptedAmounts) {
            return StepDecision.gracefulComplete(List.of(acceptedAmounts).stream().sorted().toList());
        }
    }
}
