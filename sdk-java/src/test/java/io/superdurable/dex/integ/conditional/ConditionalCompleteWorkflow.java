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

package io.superdurable.dex.integ.conditional;

import io.superdurable.dex.core.*;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.*;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;


@Component
public class ConditionalCompleteWorkflow implements ObjectWorkflow {

    public static final String SIGNAL_CHANNEL_NAME = "test-signal-channel";

    public static final String INTERNAL_CHANNEL_NAME = "test-internal-channel";
    public static final String DA_COUNTER = "counter";


    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                SignalChannelDef.create(Void.class, SIGNAL_CHANNEL_NAME),
                InternalChannelDef.create(Void.class, INTERNAL_CHANNEL_NAME)
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(Integer.class, DA_COUNTER)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new WorkflowState1())
        );
    }

    @RPC
    public void publishToInternalChannel(Context context, Persistence persistence, Communication communication) {
        communication.publishInternalChannel(INTERNAL_CHANNEL_NAME, null);
    }
}


class WorkflowState1 implements WorkflowState<Boolean> {

    @Override
    public Class<Boolean> getInputType() {
        return Boolean.class;
    }

    @Override
    public CommandRequest waitUntil(
            Context context,
            Boolean useSignal,
            Persistence persistence,
            final Communication communication) {

        if (useSignal) {
            return CommandRequest.forAnyCommandCompleted(
                    SignalCommand.create(ConditionalCompleteWorkflow.SIGNAL_CHANNEL_NAME)
            );
        } else {
            return CommandRequest.forAnyCommandCompleted(
                    InternalChannelCommand.create(ConditionalCompleteWorkflow.INTERNAL_CHANNEL_NAME)
            );
        }
    }

    @Override
    public StateDecision execute(
            Context context,
            Boolean useSignal,
            CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        Integer counter = persistence.getDataAttribute(ConditionalCompleteWorkflow.DA_COUNTER, Integer.class);
        if (counter == null) {
            counter = 0;
        }
        counter++;
        persistence.setDataAttribute(ConditionalCompleteWorkflow.DA_COUNTER, counter);

        if (context.getStateExecutionId().get().equals("WorkflowState1-1")) {
            // wait for 3 seconds so that the channel can have a new message
            try {
                Thread.sleep(3000);
            } catch (InterruptedException e) {
                throw new RuntimeException(e);
            }
        }
        if (useSignal) {
            return StateDecision.forceCompleteIfSignalChannelEmptyOrElse(counter, ConditionalCompleteWorkflow.SIGNAL_CHANNEL_NAME, WorkflowState1.class, useSignal);
        } else {
            return StateDecision.forceCompleteIfInternalChannelEmptyOrElse(counter, ConditionalCompleteWorkflow.INTERNAL_CHANNEL_NAME, WorkflowState1.class, useSignal);
        }
    }
}
