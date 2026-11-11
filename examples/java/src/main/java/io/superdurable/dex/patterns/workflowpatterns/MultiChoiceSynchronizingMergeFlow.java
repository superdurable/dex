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

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.Join;
import io.superdurable.dex.JoinRound;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import org.springframework.stereotype.Component;

import java.util.ArrayList;
import java.util.List;
import java.util.function.Function;

/** WCP-6, WCP-7, and WCP-37: the decision forks only the chosen branches, so the join waits for them. */
@Component
public class MultiChoiceSynchronizingMergeFlow
        implements Flow<MultiChoiceSynchronizingMergeFlow.NotificationSelection> {
    public static final Join<String> notificationSent =
            Join.define("notificationSent", String.class);

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
            final List<Function<JoinRound, StepMovement<?>>> branches =
                    new ArrayList<Function<JoinRound, StepMovement<?>>>();
            if (selection.sendEmail()) {
                branches.add(round -> StepMovement.of(SendEmail.class, round));
            }
            if (selection.sendSms()) {
                branches.add(round -> StepMovement.of(SendSms.class, round));
            }
            if (selection.sendPush()) {
                branches.add(round -> StepMovement.of(SendPush.class, round));
            }
            if (branches.isEmpty()) {
                return StepDecision.gracefulComplete(List.of());
            }
            return StepDecision.goToMany(notificationSent.fork(
                    context, branches, round -> StepMovement.of(SynchronizingMerge.class, round)));
        }
    }

    static final class SendEmail implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return notificationSent.endBranch(context, round, getStepType());
        }
    }

    static final class SendSms implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return notificationSent.endBranch(context, round, getStepType());
        }
    }

    static final class SendPush implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return notificationSent.endBranch(context, round, getStepType());
        }
    }

    static final class SynchronizingMerge implements Step<JoinRound> {
        @Override
        public Class<JoinRound> getInputType() {
            return JoinRound.class;
        }

        @Override
        public Wait waitFor(final Context context, final JoinRound round) {
            return Wait.until(notificationSent.forAllBranches(round));
        }

        @Override
        public StepDecision execute(final Context context, final JoinRound round) {
            return StepDecision.gracefulComplete(
                    notificationSent.getBranchResults(context, round).stream().sorted().toList());
        }
    }
}
