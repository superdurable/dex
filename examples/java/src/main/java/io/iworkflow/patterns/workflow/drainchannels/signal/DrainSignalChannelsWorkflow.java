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

package io.iworkflow.patterns.workflow.drainchannels.signal;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.communication.CommunicationMethodDef;
import io.iworkflow.core.communication.SignalChannelDef;
import io.iworkflow.core.communication.SignalCommand;
import io.iworkflow.core.communication.SignalCommandResult;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.gen.models.ChannelRequestStatus;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;
import java.util.Optional;

public class DrainSignalChannelsWorkflow implements ObjectWorkflow {
    final public static String QUEUE_SIGNAL_CHANNEL = "queueSignalChannel";

    /**
     * Returns the list of states in the workflow.
     *
     * @return a list of StateDef objects representing the workflow states.
     */
    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new ProcessSignalState())
        );
    }

    /**
     * Returns the communication schema for the workflow.
     *
     * @return a list of CommunicationMethodDef objects representing the communication schema.
     */
    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(String.class, QUEUE_SIGNAL_CHANNEL)
        );
    }
}

/**
 * This state processes the signals
 */
class ProcessSignalState implements WorkflowState<String> {

    /**
     * Returns the input type for this state.
     *
     * @return the class type Void.
     */
    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    /**
     * Defines the command request to wait for a signal to be received.
     *
     * @param context       the workflow context.
     * @param input         the input for the state.
     * @param persistence   the persistence layer.
     * @param communication the communication layer.
     * @return a CommandRequest object to wait for a signal on the QUEUE_SIGNAL_CHANNEL.
     */
    @Override
    public CommandRequest waitUntil(final Context context, final String input, final Persistence persistence, final Communication communication) {
        //The first "message" we receive is through the input when the workflow is started (see the controller).
        //If the input is null it is not the first message and we need to wait for messages in the channel.
        if (input == null) {
            return CommandRequest.forAnyCommandCompleted(
                    SignalCommand.create(DrainSignalChannelsWorkflow.QUEUE_SIGNAL_CHANNEL));
        }
        return CommandRequest.empty;
    }

    /**
     * If the signal channel is not empty it loops this state. If the channel is empty the workflow closes.
     *
     * @param context        the workflow context.
     * @param input          the input for the state.
     * @param commandResults the results of the command requests.
     * @param persistence    the persistence layer.
     * @param communication  the communication layer.
     * @return a StateDecision object to either complete the workflow or continue processing signals.
     * @throws IllegalStateException if no signal request or value is found.
     */
    @Override
    public StateDecision execute(final Context context, final String input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        if (input != null) {
            System.out.println("DrainSignalChannelsWorkflow process signal value: " + input);
        } else {

            final Optional<SignalCommandResult> result = commandResults.getAllSignalCommandResults().stream()
                    .filter(r -> r.getSignalRequestStatusEnum().equals(ChannelRequestStatus.RECEIVED))
                    .findFirst();
            if (result.isEmpty()) {
                throw new IllegalStateException("No signal request found");
            }

            final Optional<Object> data = result.get().getSignalValue();

            if (data.isEmpty()) {
                throw new IllegalStateException("No signal value found");
            }

            final String value = (String) data.get();
            System.out.println("DrainSignalChannelsWorkflow process signal value: " + value);
        }

        try {
            //sleep for 20s to add more signals.
            Thread.sleep(20000);
        } catch (final InterruptedException e) {
            throw new RuntimeException(e);
        }

        //To use this feature, QUEUE_SIGNAL_CHANNEL can only be consumed by one workflow state.
        return StateDecision.forceCompleteIfSignalChannelEmptyOrElse(DrainSignalChannelsWorkflow.QUEUE_SIGNAL_CHANNEL, ProcessSignalState.class);
    }
}
