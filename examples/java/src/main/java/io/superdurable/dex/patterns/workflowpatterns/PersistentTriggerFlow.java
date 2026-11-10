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
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-24 Persistent Trigger: a trigger stays queued until a Step consumes it. */
@Component
public class PersistentTriggerFlow implements Flow<Void> {
    public static final Channel<String> carrierReady = Channel.define("carrierReady", String.class);
    public static final Channel<String> shipmentLabel = Channel.define("shipmentLabel", String.class);

    private final AwaitCarrierReady awaitCarrierReady = new AwaitCarrierReady();
    private final AwaitShipmentLabel awaitShipmentLabel = new AwaitShipmentLabel();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(awaitCarrierReady).otherSteps(awaitShipmentLabel);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(carrierReady, shipmentLabel);
    }

    @RPC
    public void markCarrierReady(final Context context) {
        carrierReady.publish(context, "ready");
    }

    @RPC
    public void sendShipmentLabel(final Context context, final String labelId) {
        shipmentLabel.publish(context, labelId);
    }

    static final class AwaitCarrierReady implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(carrierReady.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goTo(AwaitShipmentLabel.class, null);
        }
    }

    static final class AwaitShipmentLabel implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(shipmentLabel.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(shipmentLabel.getConditionResults(context).get(0));
        }
    }
}
