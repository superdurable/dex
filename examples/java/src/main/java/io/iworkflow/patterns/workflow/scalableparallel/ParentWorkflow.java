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

package io.iworkflow.patterns.workflow.scalableparallel;

import io.iworkflow.patterns.workflow.scalableparallel.models.BatchEnqueueRequest;
import io.iworkflow.core.*;
import io.iworkflow.core.Context;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.command.BaseCommand;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.*;
import io.iworkflow.core.exceptions.WorkflowAlreadyStartedException;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.gen.models.*;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

import static io.iworkflow.patterns.workflow.scalableparallel.ChildWorkflow.PARENT_WORKFLOW_ID;
import static io.iworkflow.patterns.workflow.scalableparallel.ParentWorkflow.*;
import static io.iworkflow.patterns.workflow.scalableparallel.ParentWorkflow.*;

/**
 * Also See:
 * <a href="https://docs.google.com/document/d/1GfNcCRfUjPk8DPb_OENdgPJ6g7vEqXsQ0tZ7CQILLzc">Scalable Parallelism Control</a>
 */
public class ParentWorkflow implements ObjectWorkflow {

    // Number of parent workflows to control the concurrent child workflows
    // Total concurrent child workflows = NUM_PARENT_WORKFLOWS * CONCURRENCY_PER_PARENT_WORKFLOW
    public static final int NUM_PARENT_WORKFLOWS = 2;

    // The number of parallel child workflows that each parent workflow can control
    // Recommended to be less than 90
    public static final int CONCURRENCY_PER_PARENT_WORKFLOW = 3;

    // Maximum number of requests in the TASK_QUEUE as buffer, before processing
    // This is limited by the max history size per workflow
    // Recommended to be less than (2~10) * CONCURRENCY_PER_PARENT_WORKFLOW
    public static final int MAX_BUFFERED_TASKS = 10;

    public static final String TASK_QUEUE = "TaskQueue";
    public static final String CHILD_COMPLETE_CHANNEL_PREFIX = "ChildComplete_";
    // the child workflow IDs that the parent is waiting for completion
    public static final String DA_CURRENT_WAIT_CHILD_WFS = "CurrentWaitChildWfs";
    private final List<StateDef> stateDefs;

    public ParentWorkflow(final Client iwfClient) {
        this.stateDefs = Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new LoopForNextMessageState(iwfClient))
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<CommunicationMethodDef> getCommunicationSchema() {
        return Arrays.asList(
                InternalChannelDef.create(String.class, TASK_QUEUE),
                InternalChannelDef.createByPrefix(Void.class, CHILD_COMPLETE_CHANNEL_PREFIX)
        );
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String[].class, DA_CURRENT_WAIT_CHILD_WFS)
        );
    }

    @RPC
    public boolean enqueue(Context context, BatchEnqueueRequest request, Persistence persistence, Communication communication) {

        if (communication.getInternalChannelSize(TASK_QUEUE) + request.list().size() > MAX_BUFFERED_TASKS) {
            return false;
        }

        request.list().forEach(uuid -> communication.publishInternalChannel(TASK_QUEUE, uuid));
        return true;
    }

    @RPC
    public void completeChildWorkflow(Context context, String childWorkflowId, Communication communication) {
        // Here uses dynamic channel to wait for a child completion.
        // This is more robust because if using a single channel, it is possible that a child can send multiple signals for its completion.
        // In the case of network timeout and retry. And parent will have to dedup if so.
        // Otherwise, child have to send its workflowId in the message to tell parent to dedup.
        // Using dynamic channel with prefix and avoid this edge cases.
        communication.publishInternalChannel(CHILD_COMPLETE_CHANNEL_PREFIX + childWorkflowId, null);
    }
}

class InitState implements WorkflowState<BatchEnqueueRequest> {

    @Override
    public Class<BatchEnqueueRequest> getInputType() {
        return BatchEnqueueRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final BatchEnqueueRequest initRequest, final CommandResults commandResults, Persistence persistence, final Communication communication) {

        // Push tasks into the queue
        initRequest.list().forEach(uuid -> communication.publishInternalChannel(ParentWorkflow.TASK_QUEUE, uuid));

        return StateDecision.singleNextState(LoopForNextMessageState.class);
    }
}

class LoopForNextMessageState implements WorkflowState<Void> {

    private final Client iwfClient;

    public LoopForNextMessageState(final Client iwfClient) {
        this.iwfClient = iwfClient;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {

        String[] currentWaitChilds = persistence.getDataAttribute(DA_CURRENT_WAIT_CHILD_WFS, String[].class);
        if (currentWaitChilds == null) {
            currentWaitChilds =  new String[0];
        }

        ArrayList<BaseCommand> commands = new ArrayList<>();

        if (currentWaitChilds.length < CONCURRENCY_PER_PARENT_WORKFLOW) {
            commands.add(InternalChannelCommand.create(TASK_QUEUE));
            // otherwise, don't get a new request because the concurrency limit is reached
        }

        Arrays.stream(currentWaitChilds).forEach(
                childWfId -> commands.add(InternalChannelCommand.create(CHILD_COMPLETE_CHANNEL_PREFIX + childWfId)));
        return CommandRequest.forAnyCommandCompleted(commands.toArray(new BaseCommand[0]));
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, Persistence persistence, final Communication communication) {

        String[] currentWaitChilds = persistence.getDataAttribute(DA_CURRENT_WAIT_CHILD_WFS, String[].class);
        if (currentWaitChilds == null) {
            currentWaitChilds =  new String[0];
        }

        final ArrayList<String> newWaitList = new ArrayList<>(Arrays.stream(currentWaitChilds).toList());

        // Process all the commands
        // Note that "AnyCommandCompleted" could return more than one commands completed.
        for (int i = 0; i < commandResults.getAllInternalChannelCommandResult().size(); i++) {
            final InternalChannelCommandResult commandResult = commandResults.getAllInternalChannelCommandResult().get(i);
            final String channelName = commandResult.getChannelName();
            if (channelName.equals(TASK_QUEUE)) {
                if (commandResult.getRequestStatusEnum() == ChannelRequestStatus.RECEIVED) {
                    String request = (String) commandResult.getValue().get();

                    // start child workflow
                    String childWorkflowId = "processing-" + request;
                    try {
                        iwfClient.startWorkflow(ChildWorkflow.class, childWorkflowId,
                                3600, request,
                                WorkflowOptions.basicBuilder()
                                        .workflowAlreadyStartedOptions(
                                                new WorkflowAlreadyStartedOptions()
                                                        // ignoreAlreadyStartedError together with requestId is a special feature for idempotency
                                                        // this tell server to NOT throw WorkflowAlreadyStartedException if the started workflow is
                                                        // started by the same requestId.
                                                        // This is important for edge cases, e.g. network timeout and retry but the previous attempt succeeded.
                                                        // Without this feature, server will throw the error.
                                                        // You can catch the error to ignore, however, it will be a problem if there could be multiple parents
                                                        // attempt to start the same child workflow, because child will only report to one parent and you don't know whether
                                                        // or not this parent can wait for the child.
                                                        // With this feature, you can safely ignore the error and do not wait for the child,
                                                        // because the only only happens when the child is started by another parent(different requestId)
                                                        .ignoreAlreadyStartedError(true)
                                                        // Request ID provided by parent so it can identify whether the child is actually started and skip waiting for it to complete if not
                                                        .requestId(context.getChildWorkflowRequestId().get())

                                        )
                                        .initialDataAttribute(Map.of(PARENT_WORKFLOW_ID, context.getWorkflowId()))
                                        .workflowIdReusePolicy(IDReusePolicy.DISALLOW_REUSE)
                                        .build());

                        newWaitList.add(childWorkflowId);

                    } catch (WorkflowAlreadyStartedException e) {
                        // in this case, the childWorkflowId won't be added to newWaitList
                        System.out.println("already started by other state/workflow, ignore it -- not waiting for it");
                    }
                }
            } else if (channelName.startsWith(CHILD_COMPLETE_CHANNEL_PREFIX)) {
                if (commandResult.getRequestStatusEnum() == ChannelRequestStatus.RECEIVED) {
                    // child workflow complete channel
                    final String childWfId = channelName.substring(CHILD_COMPLETE_CHANNEL_PREFIX.length());
                    final boolean exists = newWaitList.remove(childWfId);
                    if (!exists) {
                        throw new RuntimeException("child workflow " + childWfId + " is not in the waiting list?");
                    }
                }
            } else {
                throw new RuntimeException("unexpected channel name: " + channelName);
            }
        }

        persistence.setDataAttribute(DA_CURRENT_WAIT_CHILD_WFS, newWaitList.toArray(new String[0]));

        if (newWaitList.isEmpty()) {
            return StateDecision.forceCompleteIfInternalChannelEmptyOrElse(TASK_QUEUE, LoopForNextMessageState.class);
        } else {
            // this means there are still other childWorkflows waiting to complete
            return StateDecision.singleNextState(LoopForNextMessageState.class);
        }
    }
}
