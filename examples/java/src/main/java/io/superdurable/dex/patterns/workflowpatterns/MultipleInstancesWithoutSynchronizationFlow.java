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

/** WCP-12 Multiple Instances without Synchronization: nothing waits for the instances. */
@Component
public class MultipleInstancesWithoutSynchronizationFlow implements Flow<String[]> {
    public static final Channel<String> invitationReleased =
            Channel.define("invitationReleased", String.class);

    private final RegisterAttendees registerAttendees = new RegisterAttendees();
    private final SendInvitation sendInvitation = new SendInvitation();
    private final ConfirmRegistration confirmRegistration = new ConfirmRegistration();

    @Override
    public StepList<String[]> getSteps() {
        return StepList.startStep(registerAttendees).otherSteps(sendInvitation, confirmRegistration);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(invitationReleased);
    }

    @RPC
    public void releaseInvitations(final Context context, final Integer count) {
        for (int index = 0; index < count; index++) {
            invitationReleased.publish(context, "released");
        }
    }

    static final class RegisterAttendees implements Step<String[]> {
        @Override
        public Class<String[]> getInputType() {
            return String[].class;
        }

        @Override
        public StepDecision execute(final Context context, final String[] attendeeNames) {
            final StepMovement<?>[] movements = new StepMovement<?>[attendeeNames.length + 1];
            for (int index = 0; index < attendeeNames.length; index++) {
                movements[index] = StepMovement.of(SendInvitation.class, attendeeNames[index]);
            }
            movements[attendeeNames.length] = StepMovement.of(ConfirmRegistration.class, null);
            return StepDecision.goToMany(movements);
        }
    }

    static final class SendInvitation implements Step<String> {
        @Override
        public Class<String> getInputType() {
            return String.class;
        }

        @Override
        public Wait waitFor(final Context context, final String attendeeName) {
            return Wait.until(invitationReleased.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final String attendeeName) {
            return StepDecision.gracefulComplete(attendeeName);
        }
    }

    static final class ConfirmRegistration implements Step<Void> {
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
