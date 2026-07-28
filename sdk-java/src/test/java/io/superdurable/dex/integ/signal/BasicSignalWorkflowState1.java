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

package io.superdurable.dex.integ.signal;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.SignalCommand;
import io.superdurable.dex.core.communication.SignalCommandResult;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.gen.models.ChannelRequestStatus;

import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_1;
import static io.superdurable.dex.integ.signal.BasicSignalWorkflow.SIGNAL_CHANNEL_NAME_2;

public class BasicSignalWorkflowState1 implements WorkflowState<Integer> {
    public static final String COMMAND_ID = "test-signal-id";
    
    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public CommandRequest waitUntil(
            Context context,
            Integer input,
            Persistence persistence,
            final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                SignalCommand.create(COMMAND_ID, SIGNAL_CHANNEL_NAME_1),
                SignalCommand.create(COMMAND_ID, SIGNAL_CHANNEL_NAME_2)
        );
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        SignalCommandResult signalCommandResult = commandResults.getAllSignalCommandResults().get(0);
        Integer output = input + (Integer) signalCommandResult.getSignalValue().get();

        SignalCommandResult signalCommandResult2 = commandResults.getAllSignalCommandResults().get(1);
        if (signalCommandResult2.getSignalRequestStatusEnum() != ChannelRequestStatus.WAITING) {
            throw new RuntimeException("the second signal should be waiting");
        }
        return StateDecision.singleNextState(BasicSignalWorkflowState2.class, output);
    }
}
