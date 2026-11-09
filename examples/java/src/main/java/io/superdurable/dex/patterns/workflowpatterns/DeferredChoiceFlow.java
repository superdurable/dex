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

/** WCP-16 Deferred Choice: the first external event decides the branch. */
@Component
public class DeferredChoiceFlow implements Flow<Void> {
    public static final Channel<String> offerAccepted = Channel.define("offerAccepted", String.class);
    public static final Channel<String> offerDeclined = Channel.define("offerDeclined", String.class);

    private final AwaitCustomerResponse awaitCustomerResponse = new AwaitCustomerResponse();
    private final FulfillOffer fulfillOffer = new FulfillOffer();
    private final ArchiveOffer archiveOffer = new ArchiveOffer();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(awaitCustomerResponse).otherSteps(fulfillOffer, archiveOffer);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(offerAccepted, offerDeclined);
    }

    @RPC
    public void acceptOffer(final Context context) {
        offerAccepted.publish(context, "accepted");
    }

    @RPC
    public void declineOffer(final Context context) {
        offerDeclined.publish(context, "declined");
    }

    static final class AwaitCustomerResponse implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(offerAccepted.forOne(), offerDeclined.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (!offerAccepted.getConditionResults(context).isEmpty()) {
                return StepDecision.goTo(FulfillOffer.class, null);
            }
            return StepDecision.goTo(ArchiveOffer.class, null);
        }
    }

    static final class FulfillOffer implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class ArchiveOffer implements Step<Void> {
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
