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

package io.superdurable.dex.products.engagement;

import io.superdurable.dex.Attribute;
import io.superdurable.dex.AttributeIndex;
import io.superdurable.dex.Channel;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class EngagementFlow implements Flow<EngagementInput> {
    public static final String STATUS_SEARCH_KEY = "CustomKeywordField";

    public final Attribute<String> employerId = Attribute.define("EmployerId", String.class);
    public final Attribute<String> jobSeekerId = Attribute.define("JobSeekerId", String.class);
    public final Attribute<Status> engagementStatus = Attribute.define(
            "EngagementStatus",
            Status.class,
            new AttributeIndex(AttributeIndex.Type.KEYWORD, STATUS_SEARCH_KEY));
    public final Attribute<Long> lastUpdateTimestamp =
            Attribute.define("LastUpdateTimeMillis", Long.class);
    public final Attribute<String> notes = Attribute.define("notes", String.class);
    public final Channel<Void> optOutReminder = Channel.define("OptOutReminder", Void.class);
    public final Channel<Void> completeProcess = Channel.define("CompleteProcess", Void.class);

    private final MyDependencyService service;
    private final Initialize initialize = new Initialize();
    private final ProcessTimeout processTimeout = new ProcessTimeout();
    private final Reminder reminder = new Reminder();
    private final NotifyExternalSystem notifyExternalSystem = new NotifyExternalSystem();

    public EngagementFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<EngagementInput> getSteps() {
        return StepList.startStep(initialize)
                .otherSteps(processTimeout, reminder, notifyExternalSystem);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(
                employerId,
                jobSeekerId,
                engagementStatus,
                lastUpdateTimestamp,
                notes,
                optOutReminder,
                completeProcess);
    }

    @RPC
    public RPCResult<EngagementDescription> describe(final Context context) {
        return RPCResult.of(describeEngagement(context));
    }

    @RPC
    public RPCResult<Status> decline(final Context context, final String note) {
        final Status status = engagementStatus.get(context);
        if (status != Status.INITIATED) {
            throw new IllegalStateException(
                    "can only decline an initiated engagement; current status is \""
                            + status + "\"");
        }
        updateStatus(context, Status.DECLINED, note);
        return RPCResult.of(
                Status.DECLINED,
                StepMovement.of(notifyExternalSystem, Status.DECLINED));
    }

    @RPC
    public RPCResult<Status> accept(final Context context, final String note) {
        final Status status = engagementStatus.get(context);
        if (status != Status.INITIATED && status != Status.DECLINED) {
            throw new IllegalStateException(
                    "can only accept an initiated or declined engagement; current status is \""
                            + status + "\"");
        }
        updateStatus(context, Status.ACCEPTED, note);
        completeProcess.publish(context, null);
        return RPCResult.of(
                Status.ACCEPTED,
                StepMovement.of(notifyExternalSystem, Status.ACCEPTED));
    }

    private EngagementDescription describeEngagement(final Context context) {
        return new EngagementDescription(
                employerId.get(context),
                jobSeekerId.get(context),
                notes.get(context),
                engagementStatus.get(context));
    }

    private void updateStatus(final Context context, final Status status, final String note) {
        engagementStatus.set(context, status);
        lastUpdateTimestamp.set(context, System.currentTimeMillis());
        String currentNotes = notes.get(context);
        if (currentNotes == null) {
            currentNotes = "";
        }
        if (note != null && !note.isEmpty()) {
            currentNotes += ";" + note;
        }
        notes.set(context, currentNotes);
    }

    final class Initialize implements Step<EngagementInput> {
        @Override
        public Class<EngagementInput> getInputType() {
            return EngagementInput.class;
        }

        @Override
        public StepDecision execute(final Context context, final EngagementInput input) {
            employerId.set(context, input.employerId);
            jobSeekerId.set(context, input.jobSeekerId);
            engagementStatus.set(context, Status.INITIATED);
            lastUpdateTimestamp.set(context, System.currentTimeMillis());
            notes.set(context, input.notes);
            return StepDecision.goToMulti(
                    StepMovement.of(processTimeout, null),
                    StepMovement.of(reminder, null),
                    StepMovement.of(notifyExternalSystem, Status.INITIATED));
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
            final EngagementDescription description = describeEngagement(context);
            String result = "timeout";
            if (description.currentStatus == Status.ACCEPTED) {
                result = "done";
            }
            service.updateExternalSystem(String.format(
                    "engagement from employer %s to job seeker %s finished with status %s",
                    description.employerId,
                    description.jobSeekerId,
                    description.currentStatus));
            return StepDecision.gracefulComplete(result);
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
            final Status status = engagementStatus.get(context);
            if (status != Status.INITIATED) {
                return StepDecision.deadEnd();
            }
            if (!optOutReminder.getConditionResults(context).isEmpty()) {
                updateStatus(context, status, "user opted out of reminders");
                return StepDecision.deadEnd();
            }
            final String seekerId = jobSeekerId.get(context);
            service.sendEmail(
                    seekerId,
                    "Reminder: please respond",
                    "Please respond to the engagement.");
            return StepDecision.goTo(reminder, null);
        }
    }

    final class NotifyExternalSystem implements Step<Status> {
        @Override
        public Class<Status> getInputType() {
            return Status.class;
        }

        @Override
        public StepDecision execute(final Context context, final Status status) {
            service.updateExternalSystem(String.format(
                    "notify engagement from employer %s to job seeker %s for status %s",
                    employerId.get(context),
                    jobSeekerId.get(context),
                    status));
            return StepDecision.deadEnd();
        }
    }
}
