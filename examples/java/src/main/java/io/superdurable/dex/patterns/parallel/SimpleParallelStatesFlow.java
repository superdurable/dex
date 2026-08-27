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

package io.superdurable.dex.patterns.parallel;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import org.springframework.stereotype.Component;

@Component
public class SimpleParallelStatesFlow implements Flow<JobSeeker> {
    private final Init init = new Init();
    private final SendTextMessage sendTextMessage = new SendTextMessage();
    private final SendEmail sendEmail = new SendEmail();

    @Override
    public StepList<JobSeeker> getSteps() {
        return StepList.startStep(init).otherSteps(sendTextMessage, sendEmail);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    final class Init implements Step<JobSeeker> {
        @Override
        public Class<JobSeeker> getInputType() {
            return JobSeeker.class;
        }

        @Override
        public StepDecision execute(final Context context, final JobSeeker input) {
            return StepDecision.goToMulti(
                    StepMovement.of(SendTextMessage.class, input.phoneNumber),
                    StepMovement.of(SendEmail.class, input.email));
        }
    }

    final class SendTextMessage implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final String message = "[FAKE] Sending text message to: " + input;
            System.out.println(message);
            context.recordEvent("text-message", message, String.class);
            return StepDecision.gracefulComplete();
        }
    }

    final class SendEmail implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public StepDecision execute(final Context context, final String input) {
            final String message = "[FAKE] Sending email to: " + input;
            System.out.println(message);
            context.recordEvent("email-notification", message, String.class);
            return StepDecision.gracefulComplete();
        }
    }
}
