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

package io.iworkflow.patterns.workflow.reminders;

import io.iworkflow.patterns.services.ServiceDependency;
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
import io.iworkflow.gen.models.ChannelRequestStatus;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

import static io.iworkflow.patterns.workflow.reminders.ReminderWorkflow.INTERNAL_CHANNEL_COMPLETE_PROCESS;
import static io.iworkflow.patterns.workflow.reminders.ReminderWorkflow.DA_STATUS;

@Component
public class ReminderWorkflow implements ObjectWorkflow {

    private final ServiceDependency myService;

    /**
     * Constructs a ReminderWorkflow with the specified service dependency.
     *
     * @param service the service dependency used for external interactions
     */
    public ReminderWorkflow(ServiceDependency service) {
        this.myService = service;
    }

    /**
     * Returns the list of states that define the workflow.
     *
     * @return a list of StateDef objects representing the workflow states
     */
    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new ProcessTimeoutState(myService)),
                StateDef.nonStartingState(new ReminderState(myService))
        );
    }

    /**
     * Returns the persistence schema for the workflow.
     *
     * @return a list of PersistenceFieldDef objects representing the persistence schema
     */
    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DA_STATUS)
        );
    }

    /**
     * Returns the communication schema for the workflow.
     *
     * @return a list of CommunicationMethodDef objects representing the communication schema
     */
    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, SIGNAL_NAME_OPT_OUT_REMINDER),
                InternalChannelDef.create(Void.class, INTERNAL_CHANNEL_COMPLETE_PROCESS)
        );
    }

    /**
     * Accepts the workflow, changing its status to ACCEPTED and publishing a completion signal.
     *
     * @param context the workflow context
     * @param persistence the persistence interface
     * @param communication the communication interface
     * @throws IllegalArgumentException if the current status is not INITIATED
     */
    @RPC
    public void accept(Context context, Persistence persistence, Communication communication) {
        final String currentStatus = persistence.getDataAttribute(DA_STATUS, String.class);
        if (!currentStatus.equals(Status.INITIATED.name())) {
            throw new IllegalArgumentException("can only accept in INITIATED status");
        }

        persistence.setDataAttribute(DA_STATUS, Status.ACCEPTED.name());
        communication.publishInternalChannel(INTERNAL_CHANNEL_COMPLETE_PROCESS, null);
    }

    public static final String DA_STATUS = "Status";
    public static final String SIGNAL_NAME_OPT_OUT_REMINDER = "OptOutReminder";
    public static final String INTERNAL_CHANNEL_COMPLETE_PROCESS = "CompleteProcess";
}

/**
 * InitState initializes the workflow by setting the initial status and starting the process timeout and reminder states.
 */
class InitState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        persistence.setDataAttribute(ReminderWorkflow.DA_STATUS, Status.INITIATED.name());

        return StateDecision.multiNextStates(
                StateMovement.create(ProcessTimeoutState.class),
                StateMovement.create(ReminderState.class)
        );
    }
}

/**
 * ProcessTimeoutState handles the timeout process and updating an external system.
 */
class ProcessTimeoutState implements WorkflowState<Void> {

    private ServiceDependency myService;

    /**
     * Constructs a ProcessTimeoutState with the specified service dependency.
     *
     * @param myService the service dependency used for external interactions
     */
    ProcessTimeoutState(ServiceDependency myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    /**
     * Wait for either a timeout or a completion signal.
     */
    @Override
    public CommandRequest waitUntil(Context context, Void input, Persistence persistence, Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofDays(60)), // ~2 months
                InternalChannelCommand.create(ReminderWorkflow.INTERNAL_CHANNEL_COMPLETE_PROCESS) // complete the process after accepted
        );
    }

    /**
     * Executes the timeout state, notifying the external system of the current status.
     * The status is either ACCEPTED or TIMEOUT, based on which CommandRequest being waited on happened first.
     */
    @Override
    public StateDecision execute(Context context, Void input, CommandResults commandResults, Persistence persistence, Communication communication) {
        final String currentStatus = persistence.getDataAttribute(ReminderWorkflow.DA_STATUS, String.class);
        String status = currentStatus.equals(Status.ACCEPTED.name()) ? "ACCEPTED" : "TIMEOUT";
        this.myService.updateExternalSystem("notify for status: " + status);

        return StateDecision.forceCompleteWorkflow("done");
    }
}

/**
 * ReminderState handles processing user opt-out signals, sending reminders, and looping back to wait for the next reminder or opt-out.
 */
class ReminderState implements WorkflowState<Void> {

    private ServiceDependency myService;

    ReminderState(ServiceDependency myService) {
        this.myService = myService;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    /**
     * Wait for either a timeout or an opt-out signal.
     */
    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofSeconds(5)), // 24 hours in real world, 5 seconds for demo
                SignalCommand.create(ReminderWorkflow.SIGNAL_NAME_OPT_OUT_REMINDER) // user can choose to opt out
        );
    }

    /**
     * Executes the reminder state, sending an email reminder or handling opt out signals.
     * <p>If status accepted, update external system and end the workflow state</p>
     * <p>If opt out, update external system and end the workflow state</p>
     * <p>If neither, send a reminder and loop back for another reminder or opt out</p>
     */
    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        final String currentStatus = persistence.getDataAttribute(ReminderWorkflow.DA_STATUS, String.class);
        if (currentStatus.equals(Status.ACCEPTED.name())) {
            System.out.println("Reminder state timer expired, but status already ACCEPTED");    // the process is already completed, so no need to send reminder
            return StateDecision.forceCompleteWorkflow("done");
        }

        final SignalCommandResult optOutSignalResult = commandResults.getAllSignalCommandResults().get(0);
        if (optOutSignalResult.getSignalRequestStatusEnum() == ChannelRequestStatus.RECEIVED) {
            // The user has opted out, so we should stop sending reminders
            this.myService.updateExternalSystem("user opted out - no more reminders");
            return StateDecision.forceCompleteWorkflow("done - opt out");
        }

        this.myService.sendEmail("Reminder:xxx please respond", "Hello xxx, ...");

        // loop back to wait for next reminder or opt out
        return StateDecision.singleNextState(ReminderState.class);
    }
}

enum Status {
    INITIATED,
    ACCEPTED,
}