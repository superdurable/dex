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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeLock;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
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

    // The locked execute holds the window lock through its retries, so keep the budget short.
    private static final RetryPolicy WINDOW_EXECUTE_RETRY = RetryPolicy.newBuilder()
            .initialInterval(Duration.ofSeconds(1))
            .maximumAttempts(3)
            .totalDuration(Duration.ofMinutes(1))
            .build();
    private static final Duration WINDOW_EXECUTE_TIMEOUT = Duration.ofSeconds(10);

    /** The first response or AwaitOfferResponse closes the window. Unset reads as open. */
    public static final String RESPONSE_WINDOW_OPEN = "open";
    public static final String RESPONSE_WINDOW_CLOSED = "closed";

    /** The outcomes of a customer response RPC. */
    public static final String OFFER_RESPONSE_RECORDED = "recorded";
    public static final String OFFER_RESPONSE_WINDOW_CLOSED = "window closed";

    public static final Channel<String> offerAccepted =
            Channel.define("offerAccepted", String.class);
    public static final Channel<String> offerRejected =
            Channel.define("offerRejected", String.class);
    /** Locked by both response RPCs and by AwaitOfferResponse's execute. */
    public static final Attribute<String> offerResponseWindow =
            Attribute.define("offerResponseWindow", String.class);

    private final AwaitOfferResponse awaitOfferResponse = new AwaitOfferResponse();
    private final FulfillOffer fulfillOffer = new FulfillOffer();
    private final CloseRejectedOffer closeRejectedOffer = new CloseRejectedOffer();
    private final RouteResponseBeforeExpiry routeResponseBeforeExpiry =
            new RouteResponseBeforeExpiry();
    private final ExpireOffer expireOffer = new ExpireOffer();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(awaitOfferResponse)
                .otherSteps(fulfillOffer, closeRejectedOffer, routeResponseBeforeExpiry, expireOffer);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(offerAccepted, offerRejected, offerResponseWindow);
    }

    /**
     * Records that the customer accepted the offer.
     *
     * @return {@link #OFFER_RESPONSE_WINDOW_CLOSED} after the first response or the decision
     */
    @RPC(lockAttributes = {"offerResponseWindow"})
    public RPCResult<String> acceptCustomerOffer(final Context context, final String note) {
        return publishWhileWindowOpen(context, offerAccepted, note);
    }

    /**
     * Records that the customer rejected the offer.
     *
     * @return {@link #OFFER_RESPONSE_WINDOW_CLOSED} after the first response or the decision
     */
    @RPC(lockAttributes = {"offerResponseWindow"})
    public RPCResult<String> rejectCustomerOffer(final Context context, final String note) {
        return publishWhileWindowOpen(context, offerRejected, note);
    }

    /**
     * Publishes the first response and closes the window. The check, the publish, and the close
     * commit together, because the RPC holds the window lock.
     */
    static RPCResult<String> publishWhileWindowOpen(
            final Context context, final Channel<String> response, final String note) {
        if (RESPONSE_WINDOW_CLOSED.equals(offerResponseWindow.get(context))) {
            return RPCResult.of(OFFER_RESPONSE_WINDOW_CLOSED);
        }
        response.publish(context, note);
        // The first response closes the window, so a second one gets OFFER_RESPONSE_WINDOW_CLOSED.
        offerResponseWindow.set(context, RESPONSE_WINDOW_CLOSED);
        return RPCResult.of(OFFER_RESPONSE_RECORDED);
    }

    // dex:group group-id:decide group-label:"Decide"
    // dex:explanation text:"Waits for an acceptance, a rejection, or the 3-day deadline, closes the response window, and routes on the winner."
    /** Waits for whichever event happens first and routes on it. */
    public static final class AwaitOfferResponse implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        /** Locks the response window in execute, the same lock as the response RPCs. */
        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .addExecuteLock(AttributeLock.of(offerResponseWindow))
                    .executeRetry(WINDOW_EXECUTE_RETRY)
                    .executeMethodTimeout(WINDOW_EXECUTE_TIMEOUT)
                    .build();
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
            // Every later response RPC now returns OFFER_RESPONSE_WINDOW_CLOSED.
            offerResponseWindow.set(context, RESPONSE_WINDOW_CLOSED);
            if (context.hasTimerFired()) {
                // A response committed after the Timer won but before this execute took the lock.
                if (offerAccepted.size(context) + offerRejected.size(context) > 0) {
                    return StepDecision.goTo(RouteResponseBeforeExpiry.class, offerId);
                }
                return StepDecision.goTo(ExpireOffer.class, offerId);
            }
            if (!offerAccepted.getConditionResults(context).isEmpty()) {
                return StepDecision.goTo(FulfillOffer.class, offerId);
            }
            return StepDecision.goTo(CloseRejectedOffer.class, offerId);
        }
    }

    // dex:group group-id:decide group-label:"Decide"
    // dex:explanation text:"Takes the response that the customer sent before the window closed and routes on it."
    /** Takes the response that committed before the window closed. */
    public static final class RouteResponseBeforeExpiry implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String offerId) {
            return Wait.anyOf(offerAccepted.forOne(), offerRejected.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            if (!offerAccepted.getConditionResults(context).isEmpty()) {
                return StepDecision.goTo(FulfillOffer.class, offerId);
            }
            return StepDecision.goTo(CloseRejectedOffer.class, offerId);
        }
    }

    // dex:group group-id:outcome group-label:"Outcome"
    // dex:explanation text:"Completes the Flow with the fulfilled offer."
    public static final class FulfillOffer implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            return StepDecision.gracefulComplete("fulfilled " + offerId);
        }
    }

    // dex:group group-id:outcome group-label:"Outcome"
    // dex:explanation text:"Completes the Flow with the rejected offer."
    public static final class CloseRejectedOffer implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String offerId) {
            return StepDecision.gracefulComplete("rejected " + offerId);
        }
    }

    // dex:group group-id:outcome group-label:"Outcome"
    // dex:explanation text:"Completes the Flow with the expired offer."
    public static final class ExpireOffer implements Step<String> {
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
