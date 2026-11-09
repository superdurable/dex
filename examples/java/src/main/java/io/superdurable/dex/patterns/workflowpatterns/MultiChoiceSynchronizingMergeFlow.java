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
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;

/** WCP-6, WCP-7, and WCP-37: the decision starts chosen branches and passes their count to the join. */
@Component
public class MultiChoiceSynchronizingMergeFlow
        implements Flow<MultiChoiceSynchronizingMergeFlow.NotificationSelection> {
    public static final Channel<String> notificationSent =
            Channel.define("notificationSent", String.class);

    /** Which notification branches to run. */
    public record NotificationSelection(boolean sendEmail, boolean sendSms, boolean sendPush) {
    }

    private final SelectNotificationChannels selectNotificationChannels =
            new SelectNotificationChannels();
    private final SendEmail sendEmail = new SendEmail();
    private final SendSms sendSms = new SendSms();
    private final SendPush sendPush = new SendPush();
    private final SynchronizingMerge synchronizingMerge = new SynchronizingMerge();

    @Override
    public StepList<NotificationSelection> getSteps() {
        return StepList.startStep(selectNotificationChannels)
                .otherSteps(sendEmail, sendSms, sendPush, synchronizingMerge);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(notificationSent);
    }

    static final class SelectNotificationChannels implements Step<NotificationSelection> {
        @Override
        public Class<NotificationSelection> getInputType() {
            return NotificationSelection.class;
        }

        @Override
        public StepDecision execute(final Context context, final NotificationSelection selection) {
            final List<StepMovement<?>> branches = new ArrayList<StepMovement<?>>();
            if (selection.sendEmail()) {
                branches.add(StepMovement.of(SendEmail.class, null));
            }
            if (selection.sendSms()) {
                branches.add(StepMovement.of(SendSms.class, null));
            }
            if (selection.sendPush()) {
                branches.add(StepMovement.of(SendPush.class, null));
            }
            if (branches.isEmpty()) {
                return StepDecision.gracefulComplete(List.of());
            }
            branches.add(0, StepMovement.of(SynchronizingMerge.class, branches.size()));
            return StepDecision.goToMany(branches.toArray(new StepMovement<?>[0]));
        }
    }

    static final class SendEmail implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            notificationSent.publish(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class SendSms implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            notificationSent.publish(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class SendPush implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            notificationSent.publish(context, getStepType());
            return StepDecision.deadEnd();
        }
    }

    static final class SynchronizingMerge implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public Wait waitFor(final Context context, final Integer selectedBranchCount) {
            return Wait.until(notificationSent.forN(selectedBranchCount));
        }

        @Override
        public StepDecision execute(final Context context, final Integer selectedBranchCount) {
            return StepDecision.gracefulComplete(
                    notificationSent.getConditionResults(context).stream().sorted().toList());
        }
    }
}
