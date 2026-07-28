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

package io.superdurable.dex.patterns.workflow.parentchild;

import io.superdurable.dex.patterns.workflow.scalableparallel.ChildWorkflow;
import io.superdurable.dex.core.*;
import io.superdurable.dex.core.command.CommandRequest;
import io.superdurable.dex.core.command.CommandResults;
import io.superdurable.dex.core.command.TimerCommand;
import io.superdurable.dex.core.communication.Communication;
import io.superdurable.dex.core.communication.CommunicationMethodDef;
import io.superdurable.dex.core.communication.InternalChannelCommand;
import io.superdurable.dex.core.communication.InternalChannelDef;
import io.superdurable.dex.core.exceptions.LongPollTimeoutException;
import io.superdurable.dex.core.exceptions.WorkflowAlreadyStartedException;
import io.superdurable.dex.core.persistence.Persistence;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

import static io.superdurable.dex.patterns.workflow.parentchild.ParentWorkflowV2.TASK_QUEUE;

/**
 * ParentWorkflowV2 is to demostrate how to start and wait for child workflow in a different way
 */
public class ParentWorkflowV2 implements ObjectWorkflow {

    // The number of parallel child workflows that each parent workflow can control
    // Recommended to be less than 90
    public static final int CONCURRENCY_PER_PARENT_WORKFLOW = 3;

    public static final String TASK_QUEUE = "task_queue";

    private final List<StateDef> stateDefs;

    public ParentWorkflowV2(final Client dexClient) {
        this.stateDefs = Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new LoopForNextTaskState()),
                StateDef.nonStartingState(new StartChildWorkflowState(dexClient)),
                StateDef.nonStartingState(new AwaitChildWorkflowCompletionState(dexClient))
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(Integer.class, TASK_QUEUE)
        );
    }
}

class InitState implements WorkflowState<Integer> {

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StateDecision execute(final Context context, final Integer numRequests, final CommandResults commandResults, Persistence persistence, final Communication communication) {

        for(int index = 0; index < numRequests; index++) {
            communication.publishInternalChannel(ParentWorkflowV2.TASK_QUEUE, index);
        }

        List<StateMovement> movements = new ArrayList<>();
        for (int i = 0; i < ParentWorkflowV2.CONCURRENCY_PER_PARENT_WORKFLOW; i++) {
            movements.add(StateMovement.create(LoopForNextTaskState.class));
        }
        // Start all the concurrent threads
        return StateDecision.multiNextStates(movements);
    }
}

class LoopForNextTaskState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {

        return CommandRequest.forAnyCommandCompleted(
                InternalChannelCommand.create(ParentWorkflowV2.TASK_QUEUE)
        );
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {

        Integer request = (Integer) commandResults.getAllInternalChannelCommandResult().get(0).getValue().get();

        return StateDecision.singleNextState(StartChildWorkflowState.class, request);
    }
}

class StartChildWorkflowState implements WorkflowState<Integer> {

    private final Client dexClient;

    public StartChildWorkflowState(final Client dexClient) {
        this.dexClient = dexClient;
    }

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StateDecision execute(final Context context, final Integer uuid, final CommandResults commandResults, Persistence persistence, final Communication communication) {

        // Start child workflow
        String childWorkflowId = "child-wf-" + uuid;
        try {
            dexClient.startWorkflow(ChildWorkflow.class, childWorkflowId,
                    3600, uuid.toString());
        } catch (WorkflowAlreadyStartedException e) {
            System.out.println("ignore this error because it is already started");
        }

        // This state is responsible for waiting for the child workflow to complete
        // Alternatively, if the child workflow is short running, we could just use dexClient.waitForWorkflowCompletion(...)
        return StateDecision.singleNextState(AwaitChildWorkflowCompletionState.class, new WaitForChildInput(childWorkflowId, 1));
    }
}

class AwaitChildWorkflowCompletionState implements WorkflowState<WaitForChildInput> {

    private final Client dexClient;

    public AwaitChildWorkflowCompletionState(final Client dexClient) {
        this.dexClient = dexClient;
    }

    @Override
    public Class<WaitForChildInput> getInputType() {
        return WaitForChildInput.class;
    }

    @Override
    public CommandRequest waitUntil(
            final Context context,
            final WaitForChildInput input,
            final Persistence persistence,
            final Communication communication) {

        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofSeconds(input.timerSeconds()))
        );
    }

    @Override
    public StateDecision execute(
            final Context context,
            final WaitForChildInput input,
            final CommandResults commandResults,
            Persistence persistence,
            final Communication communication) {
        try{
            dexClient.waitForWorkflowCompletion(input.childWFId());
        }catch(LongPollTimeoutException e){
            return StateDecision.singleNextState(AwaitChildWorkflowCompletionState.class,
                    // increase the timer time as backoff, cap to 10s
                    new WaitForChildInput(input.childWFId(), Math.min(input.timerSeconds() *2, 10)));
        }

        return StateDecision.singleNextState(LoopForNextTaskState.class);
    }

}