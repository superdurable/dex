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

package io.superdurable.dex.quickstart;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;

import java.time.Duration;

public final class OrderFlow implements Flow<String> {
    public static final String SHIP_DELAY_TIMER = "ship-delay";

    private final Charge charge;
    private final Ship ship;

    public OrderFlow(final ShopApi shop) {
        this.charge = new Charge(shop);
        this.ship = new Ship(shop);
    }

    @Override
    public StepList<String> getSteps() {
        return StepList.startStep(charge).otherSteps(ship);
    }

    static final class Charge implements Step<String> {
        private final ShopApi shop;

        Charge(final ShopApi shop) {
            this.shop = shop;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String orderId) {
            shop.charge(orderId, "charge-" + orderId);
            return StepDecision.goTo(Ship.class, orderId);
        }
    }

    static final class Ship implements Step<String> {
        private final ShopApi shop;

        Ship(final ShopApi shop) {
            this.shop = shop;
        }

        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String orderId) {
            return Wait.until(Timer.byDuration(Duration.ofSeconds(30), SHIP_DELAY_TIMER));
        }

        @Override
        public StepDecision execute(final Context context, final String orderId) {
            shop.ship(orderId);
            return StepDecision.gracefulComplete("shipped " + orderId);
        }
    }
}
