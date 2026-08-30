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
public class UserOnboardingFlow implements Flow<SignupForm> {
    public static final String WAITING_FOR_VERIFICATION = "waiting_for_verification";
    public static final String WAITING_FOR_TASK_1 = "waiting_for_task_1";
    public static final String WAITING_FOR_TASK_2 = "waiting_for_task_2";
    public static final String COMPLETED = "completed";

    public final Attribute<SignupForm> form = Attribute.define("Form", SignupForm.class);
    public final Attribute<String> status = Attribute.define("Status", String.class);
    public final Channel<Void> verifyEmail = Channel.define("VerifyEmail", Void.class);
    public final Channel<Void> task1Completed = Channel.define("Task1Completed", Void.class);
    public final Channel<Void> task2Completed = Channel.define("Task2Completed", Void.class);

    private final MyDependencyService service;
    private final Submit submit = new Submit();
    private final VerifyEmail verifyEmailStep = new VerifyEmail();
    private final AccomplishTask1 accomplishTask1Step = new AccomplishTask1();
    private final AccomplishTask2 accomplishTask2Step = new AccomplishTask2();

    public UserOnboardingFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<SignupForm> getSteps() {
        return StepList.startStep(submit).otherSteps(
                verifyEmailStep,
                accomplishTask1Step,
                accomplishTask2Step);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(form, status, verifyEmail, task1Completed, task2Completed);
    }

    @RPC
    public RPCResult<String> verify(final Context context) {
        if (!WAITING_FOR_VERIFICATION.equals(status.get(context))) {
            return RPCResult.of("already verified");
        }
        verifyEmail.publish(context, null);
        return RPCResult.of("verified");
    }

    @RPC
    public RPCResult<String> accomplishTask1(final Context context) {
        if (!WAITING_FOR_TASK_1.equals(status.get(context))) {
            return RPCResult.of("task 1 is not waiting");
        }
        task1Completed.publish(context, null);
        return RPCResult.of("task 1 accomplished");
    }

    @RPC
    public RPCResult<String> accomplishTask2(final Context context) {
        if (!WAITING_FOR_TASK_2.equals(status.get(context))) {
            return RPCResult.of("task 2 is not waiting");
        }
        task2Completed.publish(context, null);
        return RPCResult.of("task 2 accomplished");
    }

    final class Submit implements Step<SignupForm> {
        @Override
        public Class<SignupForm> getInputType() {
            return SignupForm.class;
        }

        @Override
        public StepDecision execute(final Context context, final SignupForm input) {
            form.set(context, input);
            service.sendEmail(input.email, "verify your email", "start your onboarding");
            return StepDecision.goTo(VerifyEmail.class, null);
        }
    }

    final class VerifyEmail implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            status.set(context, WAITING_FOR_VERIFICATION);
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofSeconds(24)),
                    verifyEmail.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final SignupForm signupForm = form.get(context);
            if (!verifyEmail.getConditionResults(context).isEmpty()) {
                service.sendEmail(signupForm.email, "complete onboarding task 1", "task 1 is ready");
                return StepDecision.goTo(AccomplishTask1.class, null);
            }
            service.sendEmail(signupForm.email, "verification reminder", "please verify your email");
            return StepDecision.goTo(VerifyEmail.class, null);
        }
    }

    final class AccomplishTask1 implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            status.set(context, WAITING_FOR_TASK_1);
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofSeconds(24)),
                    task1Completed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final SignupForm signupForm = form.get(context);
            if (!task1Completed.getConditionResults(context).isEmpty()) {
                service.sendEmail(signupForm.email, "complete onboarding task 2", "task 2 is ready");
                return StepDecision.goTo(AccomplishTask2.class, null);
            }
            service.sendEmail(signupForm.email, "task 1 reminder", "please complete onboarding task 1");
            return StepDecision.goTo(AccomplishTask1.class, null);
        }
    }

    final class AccomplishTask2 implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            status.set(context, WAITING_FOR_TASK_2);
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofSeconds(24)),
                    task2Completed.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final SignupForm signupForm = form.get(context);
            if (!task2Completed.getConditionResults(context).isEmpty()) {
                status.set(context, COMPLETED);
                service.sendEmail(signupForm.email, "onboarding complete", "welcome aboard");
                return StepDecision.gracefulComplete("onboarding completed");
            }
            service.sendEmail(signupForm.email, "task 2 reminder", "please complete onboarding task 2");
            return StepDecision.goTo(AccomplishTask2.class, null);
        }
    }
}
