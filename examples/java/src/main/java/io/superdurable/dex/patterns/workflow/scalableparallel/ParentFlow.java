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

import io.superdurable.dex.Attribute;
import io.superdurable.dex.Channel;
import io.superdurable.dex.ChannelMap;
import io.superdurable.dex.Client;
import io.superdurable.dex.Condition;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.IdReusePolicy;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RPC;
import io.superdurable.dex.RPCResult;
import io.superdurable.dex.StartFlowOptions;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Wait;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.patterns.workflow.scalableparallel.models.BatchEnqueueRequest;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;

/**
 * Also See:
 * <a href="https://docs.google.com/document/d/1GfNcCRfUjPk8DPb_OENdgPJ6g7vEqXsQ0tZ7CQILLzc">Scalable Parallelism Control</a>
 */
@Component
public class ParentFlow implements Flow<BatchEnqueueRequest> {
    public static final int NUM_PARENT_WORKFLOWS = 2;
    public static final int CONCURRENCY_PER_PARENT_WORKFLOW = 3;
    public static final int MAX_BUFFERED_TASKS = 10;

    public static final String TASK_QUEUE = "TaskQueue";
    public static final String CHILD_COMPLETE_CHANNEL_PREFIX = "ChildComplete_";
    public static final String DA_CURRENT_WAIT_CHILD_WFS = "CurrentWaitChildWfs";

    public final Channel<String> taskQueue = Channel.define(TASK_QUEUE, String.class);
    public final ChannelMap<Void> childComplete =
            ChannelMap.define(CHILD_COMPLETE_CHANNEL_PREFIX, Void.class);
    public final Attribute<String[]> currentWaitChildWfs =
            Attribute.define(DA_CURRENT_WAIT_CHILD_WFS, String[].class);

    private final ObjectProvider<Client> clientProvider;
    private final ChildFlow childFlow;

    private final Init init = new Init();
    private final LoopForNextMessage loopForNextMessage = new LoopForNextMessage();

    public ParentFlow(
            final ObjectProvider<Client> clientProvider, final ChildFlow childFlow) {
        this.clientProvider = clientProvider;
        this.childFlow = childFlow;
    }

    private Client client() {
        return clientProvider.getObject();
    }

    @Override
    public StepList<BatchEnqueueRequest> getSteps() {
        return StepList.startStep(init).otherSteps(loopForNextMessage);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(taskQueue, childComplete, currentWaitChildWfs);
    }

    @RPC
    public RPCResult<Boolean> enqueue(final Context context, final BatchEnqueueRequest request) {
        if (taskQueue.size(context) + request.list.size() > MAX_BUFFERED_TASKS) {
            return RPCResult.of(false);
        }
        for (final String uuid : request.list) {
            taskQueue.publish(context, uuid);
        }
        return RPCResult.of(true);
    }

    @RPC
    public void completeChildWorkflow(final Context context, final String childWorkflowId) {
        childComplete.publish(context, childWorkflowId, null);
    }

    final class Init implements Step<BatchEnqueueRequest> {
        @Override
        public Class<BatchEnqueueRequest> getInputType() {
            return BatchEnqueueRequest.class;
        }

        @Override
        public StepDecision execute(final Context context, final BatchEnqueueRequest initRequest) {
            for (final String uuid : initRequest.list) {
                taskQueue.publish(context, uuid);
            }
            return StepDecision.goTo(loopForNextMessage, null);
        }
    }

    final class LoopForNextMessage implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            String[] waiting = currentWaitChildWfs.get(context);
            if (waiting == null) {
                waiting = new String[0];
            }

            final List<Condition> conditions = new ArrayList<Condition>();
            if (waiting.length < CONCURRENCY_PER_PARENT_WORKFLOW) {
                conditions.add(taskQueue.forOne());
            }
            for (final String childWfId : waiting) {
                conditions.add(childComplete.forOne(childWfId));
            }
            return Wait.anyOf(conditions.toArray(new Condition[0]));
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            String[] waiting = currentWaitChildWfs.get(context);
            if (waiting == null) {
                waiting = new String[0];
            }
            final ArrayList<String> newWaitList =
                    new ArrayList<String>(Arrays.asList(waiting));

            final List<String> taskResults = taskQueue.getConditionResults(context);
            if (!taskResults.isEmpty()) {
                final String request = taskResults.get(0);
                final String childWorkflowId = "processing-" + request;
                try {
                    client().startFlow(
                            childFlow,
                            childWorkflowId,
                            request,
                            StartFlowOptions.newBuilder()
                                    .timeout(Duration.ofHours(1))
                                    .ignoreAlreadyStarted(true)
                                    .requestId(context.getStepExecutionId())
                                    .addAttribute(
                                            childFlow.parentWorkflowId,
                                            context.getFlowId())
                                    .idReusePolicy(IdReusePolicy.DISALLOW)
                                    .build());
                    newWaitList.add(childWorkflowId);
                } catch (final FlowAlreadyStartedException alreadyStarted) {
                    System.out.println(
                            "already started by other state/workflow, ignore it "
                                    + "-- not waiting for it");
                }
            }

            for (final String childWfId : new ArrayList<String>(newWaitList)) {
                if (!childComplete.getConditionResults(context, childWfId).isEmpty()) {
                    final boolean exists = newWaitList.remove(childWfId);
                    if (!exists) {
                        throw new RuntimeException(
                                "child workflow " + childWfId + " is not in the waiting list?");
                    }
                }
            }

            currentWaitChildWfs.set(context, newWaitList.toArray(new String[0]));

            if (newWaitList.isEmpty()) {
                return StepDecision.forceCompleteIfChannelsEmpty(
                        null,
                        StepMovement.of(loopForNextMessage, null),
                        taskQueue);
            }
            return StepDecision.goTo(loopForNextMessage, null);
        }
    }
}
