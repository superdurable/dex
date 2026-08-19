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

package io.superdurable.dex.products.signup;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class UserSignupFlow implements Flow<SignupForm> {
    public final Attribute<SignupForm> form = Attribute.define("Form", SignupForm.class);
    public final Attribute<String> status = Attribute.define("Status", String.class);
    public final Channel<Void> verify = Channel.define("Verify", Void.class);

    private final MyDependencyService service;
    private final Submit submit = new Submit();
    private final Verify verifyStep = new Verify();

    public UserSignupFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<SignupForm> getSteps() {
        return StepList.startStep(submit).otherSteps(verifyStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(form, status, verify);
    }

    @RPC
    public RPCResult<String> verify(final Context context) {
        if ("verified".equals(status.get(context))) {
            return RPCResult.of("already verified");
        }
        status.set(context, "verified");
        verify.publish(context, null);
        return RPCResult.of("done");
    }

    final class Submit implements Step<SignupForm> {
        @Override
        public Class<SignupForm> getInputType() {
            return SignupForm.class;
        }

        @Override
        public StepDecision execute(final Context context, final SignupForm input) {
            form.set(context, input);
            status.set(context, "waiting");
            service.sendEmail(input.email, "please verify the signup", "content");
            return StepDecision.goTo(verifyStep, null);
        }
    }

    final class Verify implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofSeconds(24)),
                    verify.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final SignupForm signupForm = form.get(context);
            if (!verify.getConditionResults(context).isEmpty()) {
                service.sendEmail(signupForm.email, "welcome", "welcome to Indeed!");
                return StepDecision.gracefulComplete("done");
            }
            service.sendEmail(signupForm.email, "reminder", "please verify your email");
            return StepDecision.goTo(verifyStep, null);
        }
    }
}
