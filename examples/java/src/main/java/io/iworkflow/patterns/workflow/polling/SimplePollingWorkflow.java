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

package io.iworkflow.patterns.workflow.polling;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandRequest;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.command.TimerCommand;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;

import java.time.Duration;
import java.util.Arrays;
import java.util.List;

/**
 * A simple polling workflow that periodically checks the readiness of an external system.
 * <p>This workflow is designed to repeatedly poll an external system at regular intervals until the system is confirmed to be ready.</p>
 * <p>Once the system is ready, the workflow transitions to a completion state and gracefully concludes the process.</p>
 *
 * <h2>Usage</h2>
 * <p>To start the Simple Polling Workflow, use the following REST endpoint:</p>
 * <pre>
 * GET /design-pattern/polling/start/simple?workflowId={workflowId}
 * </pre>
 * <p>This endpoint initiates the workflow with the specified {@code workflowId}.</p>
 *
 * <h2>Workflow States</h2>
 * <ul>
 *   <li><b>SimplePollingState</b>: The initial state that performs the polling operation.</li>
 *   <li><b>SimplePollingCompleteState</b>: The final state that completes the workflow once the system is ready.</li>
 * </ul>
 *
 * <p>This workflow serves as a template for implementing polling mechanisms in scenarios where external system readiness is a prerequisite for further processing.</p>
 */
public class SimplePollingWorkflow implements ObjectWorkflow {
    private final List<StateDef> stateDefs;

    public SimplePollingWorkflow() {
        this.stateDefs = Arrays.asList(
                StateDef.startingState(new SimplePollingState()),
                StateDef.nonStartingState(new SimplePollingCompleteState())
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }
}

/**
 * The first state in the workflow that will poll the external system until it is ready.
 */
class SimplePollingState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public CommandRequest waitUntil(Context context, Void input, Persistence persistence, Communication communication) {
        return CommandRequest.forAnyCommandCompleted(
                TimerCommand.createByDuration(Duration.ofSeconds(10))   // 10 seconds for demonstration, it can be any duration
        );
    }

    @Override
    public StateDecision execute(
            Context context,
            Void input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {

        boolean systemReady = isSystemReady(); // check dependency for readiness

        if(systemReady){
            // the system is now ready so we can move to the next state to complete our workflow
            return StateDecision.singleNextState(SimplePollingCompleteState.class, null);
        } else {
            // the system is not ready so we need to loop back and wait another cycle
            return StateDecision.singleNextState(SimplePollingState.class, null);
        }
    }

    /**
     * Mock method to simulate a check for system readiness.
     * <p>This method is intended to be a placeholder for an actual readiness check in a production environment.</p>
     * <p>In a real-world application, replace this method with logic that verifies the readiness of an external system,
     * such as making an API call or querying a service to determine if it is operational and ready for further processing.</p>
     * <p>For demonstration purposes, this mock implementation always returns {@code true}.</p>
     */
    private boolean isSystemReady() {
        System.out.println("Executing external system check for readiness...");
        return true;
    }
}

/**
 * Represents the final state in the Simple Polling Workflow.
 * <p>This state is executed once the {@code SimplePollingState} confirms that the external system is ready.</p>
 * <p>Upon execution, this state would perform any necessary finalization tasks and gracefully completes the workflow
 * e.g. any final notifications or logging are performed.</p>
 */
class SimplePollingCompleteState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public StateDecision execute(
            Context context,
            Void input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        System.out.println("Executing final state to complete the workflow...");

        return StateDecision.gracefulCompleteWorkflow();
    }
}
