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

package io.superdurable.dex.integ.internalchannel;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.core.communication.InternalChannelCommandResult;
import io.superdurable.dex.core.persistence.Persistence;

public class WaitingInternalChannelWorkflowState implements WorkflowState<Integer> {

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
        return CommandRequest.forAllCommandCompleted(
                InternalChannelCommand.create(WaitingInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME),
                InternalChannelCommand.create(WaitingInternalChannelWorkflow.INTER_STATE_CHANNEL_NAME)
        );
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer input,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        final InternalChannelCommandResult result1 = commandResults.getAllInternalChannelCommandResult().get(0);
        final InternalChannelCommandResult result2 = commandResults.getAllInternalChannelCommandResult().get(1);

        Integer output = input + (Integer) result1.getValue().get() + (Integer) result2.getValue().get();

        return StateDecision.gracefulCompleteWorkflow(output);
    }
}
