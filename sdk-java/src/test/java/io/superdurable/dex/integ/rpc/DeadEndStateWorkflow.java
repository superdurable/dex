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

package io.superdurable.dex.integ.rpc;

import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.RPC;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.StateMovement;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.InternalChannelDef;
import io.superdurable.dex.core.communication.SignalChannelDef;
import io.superdurable.dex.core.persistence.Persistence;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.integ.RpcTest.RPC_OUTPUT;

@Component
public class DeadEndStateWorkflow implements ObjectWorkflow {

    public static final String IDLE_INTERNAL_CHANNEL = "ideal-internal-channel";
    public static final String IDLE_SIGNAL_CHANNEL = "ideal-signal-channel";
    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(Void.class, IDLE_INTERNAL_CHANNEL),
                SignalChannelDef.create(Void.class, IDLE_SIGNAL_CHANNEL)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new DeadEndState()),
                StateDef.nonStartingState(new RpcWorkflowState2())
        );
    }

    @RPC
    public int getSignalChannelSize(Context context, Persistence persistence, Communication communication) {
        return communication.getSignalChannelSize(IDLE_SIGNAL_CHANNEL);
    }

    @RPC
    public int sendAndGetInternalChannelSize(Context context,  Persistence persistence, Communication communication) {
        communication.publishInternalChannel(IDLE_INTERNAL_CHANNEL, null);
        return communication.getInternalChannelSize(IDLE_INTERNAL_CHANNEL);
    }
    @RPC
    public Long testRpcFunc1(Context context, String input, Persistence persistence, Communication communication) {
        if (context.getWorkflowId().isEmpty() || context.getWorkflowRunId().isEmpty() ||
                context.getWorkflowType().isEmpty() || !context.getWorkflowType().equals("DeadEndStateWorkflow") ) {
            throw new RuntimeException("invalid context");
        }
        communication.triggerStateMovements(StateMovement.create(RpcWorkflowState2.class));
        return RPC_OUTPUT;
    }
}

class DeadEndState implements WorkflowState<Void>{

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        return StateDecision.deadEnd();
    }
}
