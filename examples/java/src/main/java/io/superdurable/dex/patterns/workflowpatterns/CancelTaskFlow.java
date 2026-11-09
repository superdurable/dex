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
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

/** WCP-19 Cancel Task: an RPC withdraws one waiting Step and starts an alternative. */
@Component
public class CancelTaskFlow implements Flow<Void> {
    public static final Channel<String> courierArrived = Channel.define("courierArrived", String.class);

    private final AwaitCourierPickup awaitCourierPickup = new AwaitCourierPickup();
    private final ArrangeSelfDelivery arrangeSelfDelivery = new ArrangeSelfDelivery();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(awaitCourierPickup).otherSteps(arrangeSelfDelivery);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(courierArrived);
    }

    @RPC
    public void confirmCourierPickup(final Context context) {
        courierArrived.publish(context, "arrived");
    }

    @RPC
    @SuppressWarnings("unchecked")
    public RPCResult<Void> cancelCourierPickup(final Context context) {
        return RPCResult.of((Void) null, StepMovement.of(ArrangeSelfDelivery.class, null))
                .withCancelingSteps(AwaitCourierPickup.class);
    }

    static final class AwaitCourierPickup implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(courierArrived.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class ArrangeSelfDelivery implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }
}
