/*
 * Copyright (c) 2026 Super Durable, Inc.
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

package io.superdurable.dex.primitives.stepdecision;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public final class StepDecisionFlow implements Flow<String> {
    private final RouteStep route = new RouteStep();
    private final CarrierAStep carrierA = new CarrierAStep();
    private final CarrierBStep carrierB = new CarrierBStep();
    private final WinnerStep winner = new WinnerStep();
    private final RecordQuoteStep recordQuote = new RecordQuoteStep();
    private final BranchWorkerStep branchWorker = new BranchWorkerStep();

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(route)
                .otherSteps(carrierA, carrierB, winner, recordQuote, branchWorker);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    public static final class Quote {
        private final String carrier;
        private final int price;

        public Quote(final String carrier, final int price) {
            this.carrier = carrier;
            this.price = price;
        }

        public String getCarrier() {
            return carrier;
        }

        public int getPrice() {
            return price;
        }
    }

    final class RouteStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String mode) {
            switch (mode) {
                case "graceful":
                    return StepDecision.gracefulComplete("done");
                case "dead-end":
                    return StepDecision.goToMulti(
                            StepMovement.of(branchWorker, "left"),
                            StepMovement.of(branchWorker, "right"));
                default:
                    final Quote quote = new Quote("winner", 9);
                    return StepDecision.goToMulti(
                            StepMovement.of(carrierA, new Quote("A", 10)),
                            StepMovement.of(carrierB, new Quote("B", 12)),
                            StepMovement.of(winner, quote));
            }
        }
    }

    static final class BranchWorkerStep implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            return StepDecision.deadEnd();
        }
    }

    static final class CarrierAStep implements Step<Quote> {
        @Override
        public Class<Quote> getInputType() {
            return Quote.class;
        }

        @Override
        public Wait waitFor(final Context context, final Quote input) {
            return Wait.anyOf(Timer.byDuration(Duration.ofSeconds(2)));
        }

        @Override
        public StepDecision execute(final Context context, final Quote input) {
            return StepDecision.deadEnd();
        }
    }

    static final class CarrierBStep implements Step<Quote> {
        @Override
        public Class<Quote> getInputType() {
            return Quote.class;
        }

        @Override
        public Wait waitFor(final Context context, final Quote input) {
            return Wait.anyOf(Timer.byDuration(Duration.ofSeconds(2)));
        }

        @Override
        public StepDecision execute(final Context context, final Quote input) {
            return StepDecision.deadEnd();
        }
    }

    final class WinnerStep implements Step<Quote> {
        @Override
        public Class<Quote> getInputType() {
            return Quote.class;
        }

        @Override
        public StepDecision execute(final Context context, final Quote quote) {
            return StepDecision.goTo(recordQuote, quote)
                    .withCancelingSteps(carrierA, carrierB);
        }
    }

    final class RecordQuoteStep implements Step<Quote> {
        @Override
        public Class<Quote> getInputType() {
            return Quote.class;
        }

        @Override
        public StepDecision execute(final Context context, final Quote quote) {
            return StepDecision.gracefulComplete(quote);
        }
    }
}
