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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.ServiceDependency;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class ReminderFlow implements Flow<Void> {
    public static final String OPT_OUT_CHANNEL = "OptOut";

    public final Channel<Void> optOut = Channel.define(OPT_OUT_CHANNEL, Void.class);

    private final ServiceDependency service;
    private final ReminderStep reminderStep = new ReminderStep();

    public ReminderFlow(final ServiceDependency service) {
        this.service = service;
    }

    @Override
    public StepList<Void> getSteps() {
        return StepList.startStep(reminderStep);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(optOut);
    }

    final class ReminderStep implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(
                    Timer.byDuration(Duration.ofSeconds(5)),
                    optOut.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            if (!context.hasTimerFired()) {
                return StepDecision.gracefulComplete();
            }
            service.sendEmail("Reminder: please respond", "Hello, ...");
            return StepDecision.goTo(ReminderStep.class, null);
        }
    }
}
