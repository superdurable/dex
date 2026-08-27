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

package io.superdurable.dex.patterns.reminders;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.ServiceDependency;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class ReminderFlow implements Flow<Void> {
    public static final String DA_STATUS = "Status";
    public static final String SIGNAL_NAME_OPT_OUT_REMINDER = "OptOutReminder";
    public static final String INTERNAL_CHANNEL_COMPLETE_PROCESS = "CompleteProcess";

    public final Attribute<String> status = Attribute.define(DA_STATUS, String.class);
    public final Channel<Void> optOutReminder =
            Channel.define(SIGNAL_NAME_OPT_OUT_REMINDER, Void.class);
    public final Channel<Void> completeProcess =
            Channel.define(INTERNAL_CHANNEL_COMPLETE_PROCESS, Void.class);

    private final ServiceDependency myService;
    private final Init init = new Init();
    private final ProcessTimeout processTimeout = new ProcessTimeout();
    private final Reminder reminder = new Reminder();

    public ReminderFlow(final ServiceDependency service) {
        this.myService = service;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(init).otherSteps(processTimeout, reminder);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(status, optOutReminder, completeProcess);
    }

    @RPC
    public void accept(final Context context) {
        final String currentStatus = status.get(context);
        if (!Status.INITIATED.name().equals(currentStatus)) {
            throw new IllegalArgumentException("can only accept in INITIATED status");
        }
        status.set(context, Status.ACCEPTED.name());
        completeProcess.publish(context, null);
    }

    final class Init implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            status.set(context, Status.INITIATED.name());
            return StepDecision.goToMulti(
                    StepMovement.of(ProcessTimeout.class, null),
                    StepMovement.of(Reminder.class, null));
        }
    }

    final class ProcessTimeout implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofDays(60)),
                    completeProcess.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String currentStatus = status.get(context);
            final String resultStatus =
                    Status.ACCEPTED.name().equals(currentStatus) ? "ACCEPTED" : "TIMEOUT";
            myService.updateExternalSystem("notify for status: " + resultStatus);
            return StepDecision.forceComplete("done");
        }
    }

    final class Reminder implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofSeconds(5)),
                    optOutReminder.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final String currentStatus = status.get(context);
            if (Status.ACCEPTED.name().equals(currentStatus)) {
                System.out.println(
                        "Reminder state timer expired, but status already ACCEPTED");
                return StepDecision.forceComplete("done");
            }

            if (!optOutReminder.getConditionResults(context).isEmpty()) {
                myService.updateExternalSystem("user opted out - no more reminders");
                return StepDecision.forceComplete("done - opt out");
            }

            myService.sendEmail("Reminder:xxx please respond", "Hello xxx, ...");
            return StepDecision.goTo(Reminder.class, null);
        }
    }
}

enum Status {
    INITIATED,
    ACCEPTED,
}
