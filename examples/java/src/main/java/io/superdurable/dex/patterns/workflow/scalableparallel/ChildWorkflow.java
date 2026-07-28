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

package io.superdurable.dex.patterns.workflow.scalableparallel;

import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.exceptions.NoRunningWorkflowException;
import io.superdurable.dex.core.persistence.DataAttributeDef;
import io.superdurable.dex.core.persistence.Persistence;
import io.superdurable.dex.core.persistence.PersistenceFieldDef;

import java.time.Duration;
import java.util.List;
import java.util.Random;

/**
 * A workflow of processing a task
 */
public class ChildWorkflow implements ObjectWorkflow {

    public static final String PARENT_WORKFLOW_ID = "ParentWorkflowId";

    private final List<StateDef> stateDefs;

    public ChildWorkflow(Client dexClient) {
        this.stateDefs = List.of(
                StateDef.startingState(new ProcessingState(dexClient))
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return List.of(
                DataAttributeDef.create(String.class, PARENT_WORKFLOW_ID)
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

}

class ProcessingState implements WorkflowState<String> {

    private final Client dexClient;

    public ProcessingState(Client dexClient) {
        this.dexClient = dexClient;
    }

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final String input, final Persistence persistence, final Communication communication) {
        final int random = new Random().nextInt(60);
        return CommandRequest.forAnyCommandCompleted(
                // Timer to simulate a long running process
                TimerCommand.createByDuration(Duration.ofSeconds(random))
        );
    }

    @Override
    public StateDecision execute(final Context context, final String input, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        // This is set by startWorkflow WorkflowOptions as initial data attribute
        // It can also be passed by startWorkflow request, but here is to demonstrate how to use initial data attribute for convenience
        final String parentWorkflowId = persistence.getDataAttribute(ChildWorkflow.PARENT_WORKFLOW_ID, String.class);

        final ParentWorkflow stub = dexClient.newRpcStub(ParentWorkflow.class, parentWorkflowId);
        try {
            dexClient.invokeRPC(stub::completeChildWorkflow, context.getWorkflowId());
        } catch (NoRunningWorkflowException e) {
            System.out.println("Parent workflow may have completed, might be duplicate completion request, ignore it.");
        }

        return StateDecision.gracefulCompleteWorkflow();
    }
}