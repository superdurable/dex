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

package io.iworkflow.patterns.workflow.intervention;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
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

import java.util.Arrays;
import java.util.List;
import java.util.Optional;
import java.util.Set;
import java.util.stream.Collectors;

import static io.iworkflow.patterns.workflow.intervention.ManualInterventionWorkflow.INTERNAL_CHANNEL_COMMAND;
import static io.iworkflow.patterns.workflow.intervention.ManualInterventionWorkflow.NUMBER_OF_RETRIES;
import static io.iworkflow.patterns.workflow.intervention.ManualInterventionWorkflow.SIGNAL_CHANNEL_COMMAND_RETRY;

/**
 * The ManualInterventionWorkflow is designed to handle scenarios where an API call might fail, allowing for manual intervention to
 * retry or skip the operation. <p>For example, if a call to an external api fails that requires a human to fix you
 * may want to retry the call after it's fixed or you may want to skip the call and move forward with the workflow.
 * It supports communication through internal and signal channels and has a data attribute of retry attempts.</p>
 *
 * <h2>Workflow Overview</h2>
 * <p>This workflow includes Init, GetData, Error, and Final.</p>
 * <p>Workflow States:</p>
 * <ul>
 *   <li>{@link InitState}: Initializes the workflow and sets the number of retries to zero.</li>
 *   <li>{@link GetDataState}: Waits for incoming data and attempts an API call. Transitions to ErrorState
 *       if an exception occurs.</li>
 *   <li>{@link ErrorState}: Handles errors by waiting for a retry or skip signal. Transitions back to
 *       GetDataState on retry or to FinalState on skip.</li>
 *   <li>{@link FinalState}: Completes the workflow and returns the number of retries.</li>
 * </ul>
 *
 * <p>Communication Channels:</p>
 * <ul>
 *   <li>Internal Channel: {@code internal_channel_command} for receiving data. Suppose to simulate an api call</li>
 *   <li>Signal Channels: {@code signal_channel_command_retry} and {@code signal_channel_command_skip} for
 *       handling manual intervention signals.</li>
 * </ul>
 *
 * <p>Persistence:</p>
 * <ul>
 *   <li>Data Attribute: {@code number_of_retries} to track the number of retry attempts.</li>
 * </ul>
 *
 * <p>Usage:</p>
 * <p>To start the ManualInterventionWorkflow, use the following REST endpoint:</p>
 * <pre>
 *    GET /design-pattern/intervention/start?workflowId={workflowId}
 * </pre>
 * @see InitState
 * @see GetDataState
 * @see ErrorState
 * @see FinalState
 */
public class ManualInterventionWorkflow implements ObjectWorkflow {
    private final List<StateDef> stateDefs;
    public static final String  INTERNAL_CHANNEL_COMMAND = "internal_channel_command";
    public static final String SIGNAL_CHANNEL_COMMAND_RETRY = "signal_channel_command_retry";
    public static final String SIGNAL_CHANNEL_COMMAND_SKIP = "signal_channel_command_skip";
    public static final String NUMBER_OF_RETRIES = "number_of_retries";

    public ManualInterventionWorkflow() {
        this.stateDefs = Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new GetDataState()),
                StateDef.nonStartingState(new ErrorState()),
                StateDef.nonStartingState(new FinalState())
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(String.class, INTERNAL_CHANNEL_COMMAND),
                SignalChannelDef.create(Void.class, SIGNAL_CHANNEL_COMMAND_RETRY),
                SignalChannelDef.create(Void.class, SIGNAL_CHANNEL_COMMAND_SKIP)
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(Integer.class, NUMBER_OF_RETRIES)
        );
    }
}

/**
 * The {@code InitState} class represents the beginning state in this workflow.
 *
 * <p>During execution, this state initializes the workflow by setting the
 * {@code NUMBER_OF_RETRIES} data attribute to zero in the persistence layer.
 * After performing this initialization, the state transitions to the {@code GetDataState}
 * class as the next state in the workflow.</p>
 *
 * @see WorkflowState
 * @see GetDataState
 */
class InitState implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(
            final Context context,
            final Void input,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication
    ) {
        persistence.setDataAttribute(ManualInterventionWorkflow.NUMBER_OF_RETRIES, 0);
        return StateDecision.singleNextState(GetDataState.class, false);
    }
}

/**
 * The {@code GetDataState} class represents a state that calls an external api.
 *
 * <p>Upon execution, if the data received is "failed", a non-retryable exception is thrown,
 * transitioning the workflow to an error state. Otherwise, the workflow proceeds to the final state.
 *
 * <p>Retries are tracked using a persistence mechanism to increment the retry count each time
 * the state is executed with a retry flag.
 *
 * You can signal this workflow from the Temporal Cloud UI. Example:
 * <pre>
 * Signal name: __IwfSystem_ExecuteRpc
 * Data:
 * {
 *   "InterStateChannelPublishing": [
 *     {
 *       "channelName": "internal_channel_command",
 *       "value": {
 *         "encoding": "json",
 *         "data": "\"failed\""
 *       }
 *     }
 *   ]
 * }
 * </pre>
 * <p>If you want to fail the "external call" leave "failed". If you want it to succeed update the value
 * to something else.
 * @param <Boolean> The type of input expected by this state, which is a {@code Boolean} indicating
 *                  whether the state is being retried.
 */
class GetDataState implements WorkflowState<Boolean> {

    @Override
    public Class<Boolean> getInputType() {
        return Boolean.class;
    }

    @Override
    public CommandRequest waitUntil(
            final Context context,
            final Boolean isRetry,
            final Persistence persistence,
            final Communication communication
    ) {
        System.out.println("Waiting for incoming data");
        return CommandRequest.forAllCommandCompleted(InternalChannelCommand.create(ManualInterventionWorkflow.INTERNAL_CHANNEL_COMMAND));
    }

    @Override
    public StateDecision execute(
            final Context context,
            final Boolean isRetry,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication
    ) {
        if (isRetry) {
            final Integer retries = persistence.getDataAttribute(ManualInterventionWorkflow.NUMBER_OF_RETRIES, Integer.class);
            persistence.setDataAttribute(ManualInterventionWorkflow.NUMBER_OF_RETRIES, retries + 1);
        }
        try {
            pretendApiCall(commandResults);
        } catch (final Exception e) {
            return StateDecision.singleNextState(ErrorState.class);
        }
        return StateDecision.singleNextState(FinalState.class);
    }

    private void pretendApiCall(final CommandResults commandResults) {
        final Optional<Object> getDataResult = commandResults.getAllInternalChannelCommandResult().get(0).getValue();
        if (getDataResult.isPresent() && getDataResult.get() instanceof final String data) {
            System.out.println("Received data result: " + getDataResult.get());
            if (data.equals("failed")) {
                throw new IllegalArgumentException("Non-retryable exception");
            }
        }
    }
}

/**
 * The {@code ErrorState} class represents a state in the workflow where an error has occurred,
 * and manual intervention is required to proceed. This state waits for a signal command to either
 * retry the operation or skip to the final state.
 *
 * <p>In the {@code waitUntil} method, the state listens for two possible signal commands:
 * <ul>
 *   <li>{@code SIGNAL_CHANNEL_COMMAND_RETRY}: Indicates that the operation should be retried.</li>
 *   <li>{@code SIGNAL_CHANNEL_COMMAND_SKIP}: Indicates that the workflow should skip to the final state.</li>
 * </ul>
 * </p>
 * You can send a signal from the Temporal Cloud UI. Adding a rpc to the workflow
 * is another, programmatic way to add a manual intervention or you can also use iwf's built-in endpoint.
 * Go to the workflow in the Temporal Cloud UI and
 * select "Send a Signal".
 * <pre>
 * Signal name: signal_channel_command_retry
 * </pre>
 * or
 * <pre>
 * Signal name: signal_channel_command_skip
 * </pre>
 *
 * for more info on how to send a signal from the UI.
 * <p>The {@code execute} method processes the received signal commands and transitions the workflow
 * to the appropriate next state based on the received signal:
 * <ul>
 *   <li>If the retry signal is received, the workflow transitions to the {@code GetDataState}.</li>
 *   <li>If any other signal is received, the workflow transitions to the {@code FinalState}.</li>
 * </ul>
 * </p>
 */
class ErrorState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public CommandRequest waitUntil(
            final Context context,
            final Void input,
            final Persistence persistence,
            final Communication communication
    ) {
        return CommandRequest.forAnyCommandCompleted(SignalCommand.create(ManualInterventionWorkflow.SIGNAL_CHANNEL_COMMAND_RETRY),
                SignalCommand.create(ManualInterventionWorkflow.SIGNAL_CHANNEL_COMMAND_SKIP));
    }

    @Override
    public StateDecision execute(
            final Context context,
            final Void input,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication
    ) {
        final Set<String> getDataResult = commandResults.getAllSignalCommandResults().stream().filter(r -> r.getSignalRequestStatusEnum() == ChannelRequestStatus.RECEIVED).map(SignalCommandResult::getSignalChannelName).collect(Collectors.toSet());
        System.out.println("signal received: " + getDataResult);
        if (getDataResult.contains(ManualInterventionWorkflow.SIGNAL_CHANNEL_COMMAND_RETRY)) {
            return StateDecision.singleNextState(GetDataState.class, true);
        } else {
            return StateDecision.singleNextState(FinalState.class);
        }
    }
}

/**
 * This is the final state in a workflow where no further actions are required.
 * This state is responsible for gracefully completing the workflow and returning
 * the number of retries that occurred during the workflow execution.
 *
 * <p>Upon execution, this state retrieves the number of retries from the persistence
 * layer and completes the workflow with a message indicating the total number of retries.</p>
 *
 */
class FinalState implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public StateDecision execute(
            final Context context,
            final Void input,
            final CommandResults commandResults,
            final Persistence persistence,
            final Communication communication
    ) {
        final Integer numberOfRetries = persistence.getDataAttribute(ManualInterventionWorkflow.NUMBER_OF_RETRIES, Integer.class);
        return StateDecision.gracefulCompleteWorkflow("Workflow Completed. Number of retries: " + numberOfRetries);
    }
}
