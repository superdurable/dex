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

import io.superdurable.dex.Channel;
import io.superdurable.dex.Client;
import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepMovement;
import io.superdurable.dex.Timer;
import io.superdurable.dex.Wait;
import io.superdurable.dex.controller.ExampleFlows;
import io.superdurable.dex.exceptions.FlowAlreadyStartedException;
import io.superdurable.dex.exceptions.LongPollTimeoutException;
import io.superdurable.dex.patterns.workflow.scalableparallel.ChildFlow;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.stereotype.Component;

import java.time.Duration;
import java.util.ArrayList;
import java.util.List;

/**
 * ParentFlowV2 demonstrates starting and waiting for child flows.
 */
@Component
public class ParentFlowV2 implements Flow<Integer> {
    public static final int CONCURRENCY_PER_PARENT_WORKFLOW = 3;
    public static final String TASK_QUEUE = "task_queue";

    public final Channel<Integer> taskQueue = Channel.define(TASK_QUEUE, Integer.class);

    private final ObjectProvider<Client> clientProvider;
    private final ChildFlow childFlow;

    private final Init init = new Init();
    private final LoopForNextTask loopForNextTask = new LoopForNextTask();
    private final StartChildWorkflow startChildWorkflow = new StartChildWorkflow();
    private final AwaitChildWorkflowCompletion awaitChildWorkflowCompletion =
            new AwaitChildWorkflowCompletion();

    public ParentFlowV2(
            final ObjectProvider<Client> clientProvider, final ChildFlow childFlow) {
        this.clientProvider = clientProvider;
        this.childFlow = childFlow;
    }

    private Client client() {
        return clientProvider.getObject();
    }

    @Override
    public StepList<Integer> getSteps() {
        return StepList.startStep(init)
                .otherSteps(loopForNextTask, startChildWorkflow, awaitChildWorkflowCompletion);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of(taskQueue);
    }

    final class Init implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer numRequests) {
            for (int index = 0; index < numRequests; index++) {
                taskQueue.publish(context, index);
            }

            final List<StepMovement<?>> movements = new ArrayList<StepMovement<?>>();
            for (int i = 0; i < CONCURRENCY_PER_PARENT_WORKFLOW; i++) {
                movements.add(StepMovement.of(loopForNextTask, null));
            }
            return StepDecision.goToMulti(movements.toArray(new StepMovement<?>[0]));
        }
    }

    final class LoopForNextTask implements Step<Void> {
        @Override
        public Class<Void> getInputType() {
            return Void.class;
        }

        @Override
        public Wait waitFor(final Context context, final Void input) {
            return Wait.anyOf(taskQueue.forOne());
        }

        @Override
        public StepDecision execute(final Context context, final Void input) {
            final Integer request = taskQueue.getConditionResults(context).get(0);
            return StepDecision.goTo(startChildWorkflow, request);
        }
    }

    final class StartChildWorkflow implements Step<Integer> {
        @Override
        public Class<Integer> getInputType() {
            return Integer.class;
        }

        @Override
        public StepDecision execute(final Context context, final Integer uuid) {
            final String childWorkflowId = "child-wf-" + uuid;
            try {
                client().startFlow(
                        childFlow,
                        childWorkflowId,
                        uuid.toString(),
                        ExampleFlows.startOptions());
            } catch (final FlowAlreadyStartedException alreadyStarted) {
                System.out.println("ignore this error because it is already started");
            }
            return StepDecision.goTo(
                    awaitChildWorkflowCompletion,
                    new WaitForChildInput(childWorkflowId, 1));
        }
    }

    final class AwaitChildWorkflowCompletion implements Step<WaitForChildInput> {
        @Override
        public Class<WaitForChildInput> getInputType() {
            return WaitForChildInput.class;
        }

        @Override
        public Wait waitFor(final Context context, final WaitForChildInput input) {
            return Wait.anyOf(Timer.byDuration(Duration.ofSeconds(input.timerSeconds)));
        }

        @Override
        public StepDecision execute(final Context context, final WaitForChildInput input) {
            try {
                client().waitForFlow(
                        input.childWFId,
                        Object.class,
                        Duration.ofSeconds(Math.max(input.timerSeconds, 1)));
            } catch (final LongPollTimeoutException e) {
                return StepDecision.goTo(
                        awaitChildWorkflowCompletion,
                        new WaitForChildInput(
                                input.childWFId,
                                Math.min(input.timerSeconds * 2, 10)));
            }
            return StepDecision.goTo(loopForNextTask, null);
        }
    }
}
