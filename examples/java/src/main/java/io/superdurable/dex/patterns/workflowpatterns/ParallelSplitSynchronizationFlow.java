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

/** WCP-2 Parallel Split and WCP-3 Synchronization: two branches run, then one join fires. */
@Component
public class ParallelSplitSynchronizationFlow implements Flow<Void> {
    public static final Channel<String> fulfillmentBranchCompleted =
            Channel.define("fulfillmentBranchCompleted", String.class);
    public static final Channel<String> paymentApproved =
            Channel.define("paymentApproved", String.class);
    private static final int BRANCH_COUNT = 2;

    private final SplitOrder splitOrder = new SplitOrder();
    private final PackItems packItems = new PackItems();
    private final ChargePayment chargePayment = new ChargePayment();
    private final SynchronizeFulfillment synchronizeFulfillment = new SynchronizeFulfillment();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(splitOrder)
                .otherSteps(packItems, chargePayment, synchronizeFulfillment);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(fulfillmentBranchCompleted, paymentApproved);
    }

    @RPC
    public void approvePayment(final Context context) {
        paymentApproved.publish(context, "approved");
    }

    static final class SplitOrder implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(PackItems.class, null),
                    StepMovement.of(ChargePayment.class, null),
                    StepMovement.of(SynchronizeFulfillment.class, null));
        }
    }

    static final class PackItems implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            fulfillmentBranchCompleted.publish(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class ChargePayment implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(paymentApproved.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            fulfillmentBranchCompleted.publish(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class SynchronizeFulfillment implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(fulfillmentBranchCompleted.forN(BRANCH_COUNT));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(
                    fulfillmentBranchCompleted.getConditionResults(context).stream().sorted().toList());
        }
    }
}
