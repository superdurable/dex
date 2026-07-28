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

package io.iworkflow.workflow.engagement;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.RPC;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.StateMovement;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.CommunicationMethodDef;
import io.iworkflow.core.communication.InternalChannelCommand;
import io.iworkflow.core.communication.InternalChannelDef;
import io.iworkflow.core.communication.SignalChannelDef;
import io.iworkflow.core.communication.SignalCommand;
import io.iworkflow.core.communication.SignalCommandResult;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.core.persistence.SearchAttributeDef;
import io.iworkflow.gen.models.ChannelRequestStatus;
import io.iworkflow.gen.models.RetryPolicy;
import io.iworkflow.gen.models.SearchAttributeValueType;
import io.iworkflow.core.WorkflowStateOptions;
import io.iworkflow.workflow.MyDependencyService;
import io.iworkflow.workflow.engagement.model.EngagementDescription;
import io.iworkflow.workflow.engagement.model.EngagementInput;
import io.iworkflow.workflow.engagement.model.ImmutableEngagementDescription;
import io.iworkflow.workflow.engagement.model.Status;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

import static io.iworkflow.workflow.engagement.EngagementWorkflow.DA_KEY_NOTES;
import static io.iworkflow.workflow.engagement.EngagementWorkflow.INTERNAL_CHANNEL_COMPLETE_PROCESS;
import static io.iworkflow.workflow.engagement.EngagementWorkflow.SA_KEY_EMPLOYER_ID;
import static io.iworkflow.workflow.engagement.EngagementWorkflow.SA_KEY_JOB_SEEKER_ID;
import static io.iworkflow.workflow.engagement.EngagementWorkflow.SA_KEY_LAST_UPDATE_TIMESTAMP;
import static io.iworkflow.workflow.engagement.EngagementWorkflow.SA_KEY_STATUS;

@Component
public class EngagementWorkflow implements ObjectWorkflow {

    @Autowired
    private MyDependencyService myService;

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new ProcessTimeoutState(myService)),
                StateDef.nonStartingState(new ReminderState(myService)),
                StateDef.nonStartingState(new NotifyExternalSystemState(myService)));
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SA_KEY_EMPLOYER_ID),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SA_KEY_JOB_SEEKER_ID),
                SearchAttributeDef.create(SearchAttributeValueType.KEYWORD, SA_KEY_STATUS),
                SearchAttributeDef.create(SearchAttributeValueType.INT, SA_KEY_LAST_UPDATE_TIMESTAMP),

                DataAttributeDef.create(String.class, DA_KEY_NOTES));
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, SIGNAL_NAME_OPT_OUT_REMINDER),
                InternalChannelDef.create(Void.class, INTERNAL_CHANNEL_COMPLETE_PROCESS));
    }

    @RPC
    public void decline(Context context, String notes, Persistence persistence, Communication communication) {
        final String currentStatus = persistence.getSearchAttributeKeyword(SA_KEY_STATUS);
        if (!currentStatus.equals(Status.INITIATED.name())) {
            throw new IllegalArgumentException("can only decline in INITIATED status, current is " + currentStatus);
        }

        persistence.setSearchAttributeKeyword(SA_KEY_STATUS, Status.DECLINED.name());
        persistence.setSearchAttributeInt64(SA_KEY_LAST_UPDATE_TIMESTAMP, System.currentTimeMillis());
        communication.triggerStateMovements(
                StateMovement.create(NotifyExternalSystemState.class, Status.DECLINED));

        String currentNotes = persistence.getDataAttribute(DA_KEY_NOTES, String.class);
        persistence.setDataAttribute(DA_KEY_NOTES, currentNotes + ";" + notes);
    }

    @RPC
    public void accept(Context context, String notes, Persistence persistence, Communication communication) {
        final String currentStatus = persistence.getSearchAttributeKeyword(SA_KEY_STATUS);
        if (!currentStatus.equals(Status.INITIATED.name()) && !currentStatus.equals(Status.DECLINED.name())) {
            throw new IllegalArgumentException(
                    "can only accept in INITIATED or DECLINED status, current is " + currentStatus);
        }

        persistence.setSearchAttributeKeyword(SA_KEY_STATUS, Status.ACCEPTED.name());
        persistence.setSearchAttributeInt64(SA_KEY_LAST_UPDATE_TIMESTAMP, System.currentTimeMillis());
        communication.publishInternalChannel(INTERNAL_CHANNEL_COMPLETE_PROCESS, null);

        String currentNotes = persistence.getDataAttribute(DA_KEY_NOTES, String.class);
        persistence.setDataAttribute(DA_KEY_NOTES, currentNotes + ";" + notes);
    }

    @RPC
    public EngagementDescription describe(Context context, Persistence persistence, Communication communication) {
        // Note that a readOnly RPC will not write any event to history
        final String currentStatus = persistence.getSearchAttributeKeyword(SA_KEY_STATUS);
        final String employerId = persistence.getSearchAttributeKeyword(SA_KEY_EMPLOYER_ID);
        final String jobSeekerId = persistence.getSearchAttributeKeyword(SA_KEY_JOB_SEEKER_ID);

        String currentNotes = persistence.getDataAttribute(DA_KEY_NOTES, String.class);

        return ImmutableEngagementDescription.builder()
                .currentStatus(Status.valueOf(currentStatus))
                .employerId(employerId)
                .jobSeekerId(jobSeekerId)
                .notes(currentNotes)
                .build();
    }

    public static final String SA_KEY_EMPLOYER_ID = "EmployerId";
    public static final String SA_KEY_JOB_SEEKER_ID = "JobSeekerId";
    public static final String SA_KEY_STATUS = "EngagementStatus";
    public static final String SA_KEY_LAST_UPDATE_TIMESTAMP = "LastUpdateTimeMillis";
    public static final String DA_KEY_NOTES = "Notes";
    public static final String SIGNAL_NAME_OPT_OUT_REMINDER = "OptOutReminder";
    public static final String INTERNAL_CHANNEL_COMPLETE_PROCESS = "CompleteProcess";
}

class InitState implements WorkflowState<EngagementInput> {

    @Override
    public Class<EngagementInput> getInputType() {
        return EngagementInput.class;
    }

    @Override
    public StateDecision execute(final Context context, final EngagementInput input,
            final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        persistence.setSearchAttributeKeyword(SA_KEY_EMPLOYER_ID, input.getEmployerId());
        persistence.setSearchAttributeKeyword(SA_KEY_JOB_SEEKER_ID, input.getJobSeekerId());
        persistence.setSearchAttributeKeyword(SA_KEY_STATUS, Status.INITIATED.name());
        persistence.setSearchAttributeInt64(SA_KEY_LAST_UPDATE_TIMESTAMP, System.currentTimeMillis());

        persistence.setDataAttribute(DA_KEY_NOTES, input.getNotes());

        return StateDecision.multiNextStates(
                StateMovement.create(ProcessTimeoutState.class),
                StateMovement.create(ReminderState.class),
                StateMovement.create(NotifyExternalSystemState.class, Status.INITIATED));
    }
}

class ProcessTimeoutState implements WorkflowState<Void> {

    private MyDependencyService myService;

    ProcessTimeoutState(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(Context context, Void input, Persistence persistence, Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofDays(60)), // ~2 months
                InternalChannelCommand.create(INTERNAL_CHANNEL_COMPLETE_PROCESS) // complete the process after accepted
        );
    }

    @Override
    public StateDecision execute(Context context, Void input, CommandResults commandResults, Persistence persistence,
            Communication communication) {
        final String currentStatus = persistence.getSearchAttributeKeyword(SA_KEY_STATUS);
        final String employerId = persistence.getSearchAttributeKeyword(SA_KEY_EMPLOYER_ID);
        final String jobSeekerId = persistence.getSearchAttributeKeyword(SA_KEY_JOB_SEEKER_ID);

        String status = "TIMEOUT";
        if (currentStatus.equals(Status.ACCEPTED.name())) {
            status = "ACCEPTED";
        }
        this.myService.updateExternalSystem("notify engagement from employer " + employerId + " to jobSeeker "
                + jobSeekerId + " for status: " + status);

        return StateDecision.forceCompleteWorkflow("done");
    }
}

class ReminderState implements WorkflowState<Void> {

    private MyDependencyService myService;

    ReminderState(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence,
            final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofSeconds(5)), // 24 hours in real world, 5 seconds for demo
                SignalCommand.create(EngagementWorkflow.SIGNAL_NAME_OPT_OUT_REMINDER) // user can choose to opt out
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults,
            final Persistence persistence, final Communication communication) {
        final String currentStatus = persistence.getSearchAttributeKeyword(SA_KEY_STATUS);
        if (!currentStatus.equals(Status.INITIATED.name())) {
            return StateDecision.deadEnd();
        }

        final SignalCommandResult optOutSignalResult = commandResults.getAllSignalCommandResults().get(0);
        if (optOutSignalResult.getSignalRequestStatusEnum() == ChannelRequestStatus.RECEIVED) {

            String currentNotes = persistence.getDataAttribute(DA_KEY_NOTES, String.class);
            persistence.setDataAttribute(DA_KEY_NOTES, currentNotes + ";" + "User optout reminder");

            return StateDecision.deadEnd();
        }
        final String jobSeekerId = persistence.getSearchAttributeKeyword(SA_KEY_JOB_SEEKER_ID);
        this.myService.sendEmail(jobSeekerId, "Reminder:xxx please respond", "Hello xxx, ...");

        // go back to the loop
        return StateDecision.singleNextState(ReminderState.class);
    }
}

class NotifyExternalSystemState implements WorkflowState<Status> {

    private MyDependencyService myService;

    NotifyExternalSystemState(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<Status> getInputType() {
        return Status.class;
    }

    @Override
    public StateDecision execute(final Context context, final Status status, final CommandResults commandResults,
            final Persistence persistence, final Communication communication) {
        final String employerId = persistence.getSearchAttributeKeyword(SA_KEY_EMPLOYER_ID);
        final String jobSeekerId = persistence.getSearchAttributeKeyword(SA_KEY_JOB_SEEKER_ID);
        // Note that this API will fail for a few times until success
        this.myService.updateExternalSystem("notify engagement from employer " + employerId + " to jobSeeker "
                + jobSeekerId + " for status: " + status.name());
        return StateDecision.deadEnd();
    }

    /**
     * By default, all state execution will retry infinitely (until workflow
     * timeout).
     * This may not work for some dependency as we may want to retry for only a
     * certain times
     */
    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setExecuteApiRetryPolicy(
                        new RetryPolicy()
                                .backoffCoefficient(2f)
                                .maximumAttempts(100)
                                .maximumAttemptsDurationSeconds(3600)
                                .initialIntervalSeconds(3)
                                .maximumIntervalSeconds(60));
    }
}