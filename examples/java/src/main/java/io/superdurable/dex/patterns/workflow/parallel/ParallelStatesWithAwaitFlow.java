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

package io.superdurable.dex.patterns.workflow.parallel;

import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

@Component
public class ParallelStatesWithAwaitFlow implements Flow<Integer> {
    public static final String NOTIFY_CHANNEL = "test_notify_channel";

    public final Channel<String> notifyChannel =
            Channel.define(NOTIFY_CHANNEL, String.class);

    private final Starting starting = new Starting();
    private final NotifyUser notifyUser = new NotifyUser();
    private final AwaitAllUsersNotified awaitAllUsersNotified = new AwaitAllUsersNotified();

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(starting).otherSteps(notifyUser, awaitAllUsersNotified);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(notifyChannel);
    }

    final class Starting implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer countOfJobSeekers) {
            final StepMovement<?>[] movements =
                    new StepMovement<?>[countOfJobSeekers + 1];
            movements[0] = StepMovement.of(awaitAllUsersNotified, countOfJobSeekers);
            for (int i = 1; i <= countOfJobSeekers; i++) {
                movements[i] = StepMovement.of(
                        notifyUser,
                        new JobSeeker(
                                String.valueOf(i),
                                "jobseeker@indeed.com",
                                "0987654321"));
            }
            return StepDecision.goToMulti(movements);
        }
    }

    final class NotifyUser implements Step<JobSeeker> {
        @Override
        public Class<JobSeeker> getInputType() {
            return JobSeeker.class;
        }

        @Override
        public StepDecision execute(final Context context, final JobSeeker jobSeeker) {
            try {
                Thread.sleep((long) (Math.random() * 5000));
            } catch (final InterruptedException e) {
                Thread.currentThread().interrupt();
            }

            final String message = "[FAKE] Notifying user of something: " + jobSeeker.id;
            System.out.println(message);
            context.recordEvent("notification", message, String.class);
            notifyChannel.publish(context, "I sent something");
            return StepDecision.deadEnd();
        }
    }

    final class AwaitAllUsersNotified implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer countOfJobSeekers) {
            return Wait.until(notifyChannel.forN(countOfJobSeekers));
        }

        @Override
        public StepDecision execute(final Context context, final Integer countOfJobSeekers) {
            final String message =
                    String.format("[FAKE] Sent all %s notifications", countOfJobSeekers);
            context.recordEvent("sent-notifications", message, String.class);
            return StepDecision.gracefulComplete();
        }
    }
}
