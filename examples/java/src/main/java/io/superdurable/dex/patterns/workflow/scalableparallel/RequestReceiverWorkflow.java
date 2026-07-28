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

import io.superdurable.dex.patterns.workflow.scalableparallel.exceptions.EnqueueFailedException;
import io.superdurable.dex.patterns.workflow.scalableparallel.models.BatchEnqueueRequest;
import io.superdurable.dex.core.Client;
import io.superdurable.dex.core.Context;
import io.superdurable.dex.core.ObjectWorkflow;
import io.superdurable.dex.core.StateDecision;
import io.superdurable.dex.core.StateDef;
import io.superdurable.dex.core.WorkflowState;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.exceptions.NoRunningWorkflowException;
import io.superdurable.dex.core.persistence.Persistence;

import java.util.ArrayList;
import java.util.List;
import java.util.Random;
import java.util.UUID;

import static io.superdurable.dex.patterns.workflow.scalableparallel.ParentWorkflow.NUM_PARENT_WORKFLOWS;

/**
 * A workflow for handling a request to start processing items via ParentWorkflow
 */
public class RequestReceiverWorkflow implements ObjectWorkflow {

    private final List<StateDef> stateDefs;

    public RequestReceiverWorkflow(Client dexClient) {
        this.stateDefs = List.of(
                StateDef.startingState(new RequestState(dexClient))
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }
}

class RequestState implements WorkflowState<Integer> {

    private final Client dexClient;

    public RequestState(Client dexClient) {
        this.dexClient = dexClient;
    }

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StateDecision execute(final Context context, final Integer numberOfChildWfs, final CommandResults commandResults, Persistence persistence, final Communication communication) {
        final BatchEnqueueRequest request = generateTasks(numberOfChildWfs);

        // Generate a random number between 1 and NUM_PARENT_WORKFLOWS to assign child workflows
        // NOTE: This is just for demo purposes. In a real-world scenario, a nice improvement would be to assign tasks to ParentWorkflows based on the task queue size
        int randSuffix = (new Random().nextInt(NUM_PARENT_WORKFLOWS)) + 1;

        final String parentWorkflowId = "parent_workflow_" + randSuffix;

        final ParentWorkflow stub = dexClient.newRpcStub(ParentWorkflow.class, parentWorkflowId);

        // In this example, we send all tasks in one request. This may not work in real world. 
        // If there are too many tasks(e.g. 10K+), it may be better to send them in multiple batches.
        // And we should iterate through all the baches, by looping back to this state with a cursor:
        // for the first state execution, send batch1, 2nd state execution, send batch2, ... etc. until
        // all the batches are sent. 
        // Using this state loop so that whenever something fail, it will resume from the last batch as checkpoint.
        try {
            boolean success = dexClient.invokeRPC(stub::enqueue, request);
            if (!success) {
                throw new EnqueueFailedException("Enqueue failed, retry in next attempt");
            }
        } catch (NoRunningWorkflowException e) {
            dexClient.startWorkflow(ParentWorkflow.class, parentWorkflowId, 0, request);
        }

        return StateDecision.gracefulCompleteWorkflow();
    }

    // Dummy task generation based on the number passed in the request
    // Real life example would be to take a request and divide it into smaller repeatable tasks
    private static BatchEnqueueRequest generateTasks(Integer numberOfChildWfs) {
        List<String> uuids = new ArrayList<>();
        for (int i = 0; i < numberOfChildWfs; i++) {
            uuids.add(UUID.randomUUID().toString());
        }
        return new BatchEnqueueRequest(uuids);
    }
}