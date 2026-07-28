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

package io.iworkflow.patterns.workflow.timeout;

import com.google.common.collect.ImmutableList;
import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.StateMovement;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;

import java.time.Duration;
import java.util.List;

public class HandlingTimeoutWorkflow implements ObjectWorkflow {

    @Override
    public List<StateDef> getWorkflowStates() {
        return ImmutableList.of(StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new TimeoutState()),
                StateDef.nonStartingState(new TaskState()));
    }
}

class InitState implements WorkflowState<Boolean> {
    @Override
    public Class<Boolean> getInputType() {
        return Boolean.class;
    }

    @Override
    public StateDecision execute(final Context context, final Boolean workflowSuccessful, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        return StateDecision.multiNextStates(
                StateMovement.create(TimeoutState.class),
                StateMovement.create(TaskState.class, workflowSuccessful));
    }
}

class TimeoutState implements WorkflowState<Void> {
    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Void input, final Persistence persistence, final Communication communication) {
        return CommandRequest.forAnyCommandCompleted(TimerCommand.createByDuration(Duration.ofMinutes(1)));
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        //Can either log that the workflow is taking too long and use StateDecision.deadEnd() or can complete or fail the workflow here.
        // If completing or failing the workflow, force must be used so the thread running in TaskState is closed.
        return StateDecision.forceFailWorkflow("Workflow did not finish the task in time");
    }
}

class TaskState implements WorkflowState<Boolean> {
    @Override
    public Class<Boolean> getInputType() {
        return Boolean.class;
    }

    @Override
    public CommandRequest waitUntil(final Context context, final Boolean workflowSuccessful, final Persistence persistence, final Communication communication) {
        if (workflowSuccessful) {
            return CommandRequest.empty;
        } else {
            //Simulate a task taking a long time. Time is set for longer than the timer set in the waitUntil method in the TimeoutState class.
            return CommandRequest.forAnyCommandCompleted(TimerCommand.createByDuration(Duration.ofSeconds(65)));
        }
    }

    @Override
    public StateDecision execute(final Context context, final Boolean workflowSuccessful, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        //Must use force complete so the thread running in TimeoutState is closed.
        return StateDecision.forceCompleteWorkflow("Workflow completed successfully");
    }
}
