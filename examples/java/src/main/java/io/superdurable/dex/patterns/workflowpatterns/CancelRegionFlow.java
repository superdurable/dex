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

/** WCP-25 Cancel Region: one RPC withdraws several Step types; the rest of the Flow continues. */
@Component
public class CancelRegionFlow implements Flow<Void> {
    public static final Channel<String> identityVerified =
            Channel.define("identityVerified", String.class);
    public static final Channel<String> creditHistoryChecked =
            Channel.define("creditHistoryChecked", String.class);
    public static final Channel<String> sanctionsScreened =
            Channel.define("sanctionsScreened", String.class);

    private final StartApplicationChecks startApplicationChecks = new StartApplicationChecks();
    private final VerifyIdentity verifyIdentity = new VerifyIdentity();
    private final CheckCreditHistory checkCreditHistory = new CheckCreditHistory();
    private final ScreenSanctions screenSanctions = new ScreenSanctions();
    private final NotifyRejection notifyRejection = new NotifyRejection();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startApplicationChecks)
                .otherSteps(verifyIdentity, checkCreditHistory, screenSanctions, notifyRejection);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(identityVerified, creditHistoryChecked, sanctionsScreened);
    }

    @RPC
    @SuppressWarnings("unchecked")
    public RPCResult<Void> rejectApplication(final Context context) {
        return RPCResult.of((Void) null, StepMovement.of(NotifyRejection.class, null))
                .withCancelingSteps(VerifyIdentity.class, CheckCreditHistory.class);
    }

    @RPC
    public void completeSanctionsScreening(final Context context) {
        sanctionsScreened.publish(context, "screened");
    }

    static final class StartApplicationChecks implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(VerifyIdentity.class, null),
                    StepMovement.of(CheckCreditHistory.class, null),
                    StepMovement.of(ScreenSanctions.class, null));
        }
    }

    static final class VerifyIdentity implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(identityVerified.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class CheckCreditHistory implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(creditHistoryChecked.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class ScreenSanctions implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(sanctionsScreened.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class NotifyRejection implements Step<Void> {
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
