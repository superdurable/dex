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

/** WCP-29 and WCP-32: the join fires, then its decision cancels the remaining branches. */
@Component
public class CancellingPartialJoinFlow implements Flow<Integer> {
    public static final Channel<String> carrierAReleased =
            Channel.define("carrierAReleased", String.class);
    public static final Channel<String> carrierBReleased =
            Channel.define("carrierBReleased", String.class);
    public static final Channel<String> carrierCReleased =
            Channel.define("carrierCReleased", String.class);
    public static final Channel<String> bidReceived = Channel.define("bidReceived", String.class);

    private final RequestBids requestBids = new RequestBids();
    private final BidFromCarrierA bidFromCarrierA = new BidFromCarrierA();
    private final BidFromCarrierB bidFromCarrierB = new BidFromCarrierB();
    private final BidFromCarrierC bidFromCarrierC = new BidFromCarrierC();
    private final AcceptBids acceptBids = new AcceptBids();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(requestBids)
                .otherSteps(bidFromCarrierA, bidFromCarrierB, bidFromCarrierC, acceptBids);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(carrierAReleased, carrierBReleased, carrierCReleased, bidReceived);
    }

    /** Releases the bid of carrier "A", "B", or "C". */
    @RPC
    public void releaseCarrierBid(final Context context, final String carrier) {
        carrierReleasedChannel(carrier).publish(context, carrier);
    }

    private static Channel<String> carrierReleasedChannel(final String carrier) {
        if ("A".equals(carrier)) {
            return carrierAReleased;
        }
        if ("B".equals(carrier)) {
            return carrierBReleased;
        }
        if ("C".equals(carrier)) {
            return carrierCReleased;
        }
        throw new IllegalArgumentException("unknown carrier: " + carrier);
    }

    static final class RequestBids implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer requiredBids) {
            return StepDecision.goToMany(
                    StepMovement.of(BidFromCarrierA.class, null),
                    StepMovement.of(BidFromCarrierB.class, null),
                    StepMovement.of(BidFromCarrierC.class, null),
                    StepMovement.of(AcceptBids.class, requiredBids));
        }
    }

    static final class BidFromCarrierA implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(carrierAReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            bidReceived.publish(context, "A");
            return StepDecision.deadEnd();
        }
    }

    static final class BidFromCarrierB implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(carrierBReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            bidReceived.publish(context, "B");
            return StepDecision.deadEnd();
        }
    }

    static final class BidFromCarrierC implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(carrierCReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            bidReceived.publish(context, "C");
            return StepDecision.deadEnd();
        }
    }

    static final class AcceptBids implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer requiredBids) {
            return Wait.until(bidReceived.forN(requiredBids));
        }

        @Override
        @SuppressWarnings("unchecked")
        public StepDecision execute(final Context context, final Integer requiredBids) {
            return StepDecision.gracefulComplete(
                            bidReceived.getConditionResults(context).stream().sorted().toList())
                    .withCancelingSteps(
                            BidFromCarrierA.class, BidFromCarrierB.class, BidFromCarrierC.class);
        }
    }
}
