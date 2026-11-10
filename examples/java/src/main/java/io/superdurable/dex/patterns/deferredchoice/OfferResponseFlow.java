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

package io.superdurable.dex.patterns.deferredchoice;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

/**
 * Routes on the first of three events: the customer accepts, the customer rejects, or the offer
 * expires.
 */
@Component
public class OfferResponseFlow implements Flow<String> {
    /** How long the customer has to respond. */
    public static final Duration OFFER_EXPIRY = Duration.ofDays(3);

    public static final Channel<String> offerAccepted =
            Channel.define("offerAccepted", String.class);
    public static final Channel<String> offerRejected =
            Channel.define("offerRejected", String.class);

    private final AwaitOfferResponse awaitOfferResponse = new AwaitOfferResponse();
    private final FulfillOffer fulfillOffer = new FulfillOffer();
    private final CloseRejectedOffer closeRejectedOffer = new CloseRejectedOffer();
    private final ExpireOffer expireOffer = new ExpireOffer();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(awaitOfferResponse)
                .otherSteps(fulfillOffer, closeRejectedOffer, expireOffer);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(offerAccepted, offerRejected);
    }

    /** Records that the customer accepted the offer. */
    @RPC
    public void acceptCustomerOffer(final Context context, final String note) {
        offerAccepted.publish(context, note);
    }

    /** Records that the customer rejected the offer. */
    @RPC
    public void rejectCustomerOffer(final Context context, final String note) {
        offerRejected.publish(context, note);
    }

    /** Waits for whichever event happens first and routes on it. */
    static final class AwaitOfferResponse implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String offerId) {
            return Wait.anyOf(
                    offerAccepted.forOne(),
                    offerRejected.forOne(),
                    Timer.byDuration(OFFER_EXPIRY));
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            if (context.hasTimerFired()) {
                return StepDecision.goTo(ExpireOffer.class, offerId);
            }
            if (!offerAccepted.getConditionResults(context).isEmpty()) {
                return StepDecision.goTo(FulfillOffer.class, offerId);
            }
            return StepDecision.goTo(CloseRejectedOffer.class, offerId);
        }
    }

    static final class FulfillOffer implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            return StepDecision.gracefulComplete("fulfilled " + offerId);
        }
    }

    static final class CloseRejectedOffer implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            return StepDecision.gracefulComplete("rejected " + offerId);
        }
    }

    static final class ExpireOffer implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            return StepDecision.gracefulComplete("expired " + offerId);
        }
    }
}
