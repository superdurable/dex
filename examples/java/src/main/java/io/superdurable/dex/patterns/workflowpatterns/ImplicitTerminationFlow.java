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

/** WCP-11 Implicit Termination: the Flow closes only when no Step is active. */
@Component
public class ImplicitTerminationFlow implements Flow<Void> {
    public static final Channel<String> profileCompleted =
            Channel.define("profileCompleted", String.class);

    private final StartOnboarding startOnboarding = new StartOnboarding();
    private final CreateAccount createAccount = new CreateAccount();
    private final SendWelcomeEmail sendWelcomeEmail = new SendWelcomeEmail();
    private final AwaitProfileCompletion awaitProfileCompletion = new AwaitProfileCompletion();

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(startOnboarding)
                .otherSteps(createAccount, sendWelcomeEmail, awaitProfileCompletion);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(profileCompleted);
    }

    @RPC
    public void completeProfile(final Context context) {
        profileCompleted.publish(context, "completed");
    }

    static final class StartOnboarding implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.goToMany(
                    StepMovement.of(CreateAccount.class, null),
                    StepMovement.of(SendWelcomeEmail.class, null),
                    StepMovement.of(AwaitProfileCompletion.class, null));
        }
    }

    static final class CreateAccount implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class SendWelcomeEmail implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }

    static final class AwaitProfileCompletion implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.until(profileCompleted.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            return StepDecision.gracefulComplete(getStepType());
        }
    }
}
