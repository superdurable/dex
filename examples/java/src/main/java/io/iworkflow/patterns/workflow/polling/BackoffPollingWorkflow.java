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

import io.iworkflow.patterns.services.ServiceDependency;
import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.WorkflowStateOptions;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.gen.models.RetryPolicy;

import java.util.List;

/**
 * A workflow implementation that utilizes an exponential backoff strategy for polling an external dependency.
 * <p>This workflow is designed to handle scenarios where an external system or service may not be immediately available,
 * and repeated attempts with increasing intervals are necessary to achieve a successful interaction.</p>
 *
 * <h2>Workflow Overview</h2>
 * <p>The Backoff Polling Workflow consists of the following states:</p>
 * <ul>
 *   <li><b>ReadExternalDepState</b>: The initial state that attempts to read from an external dependency.
 *       It employs a retry mechanism with exponential backoff to handle transient failures.</li>
 *   <li><b>PollingCompleteState</b>: The final state that completes the workflow once the external dependency
 *       has been successfully accessed and processed.</li>
 * </ul>
 *
 * <h2>Retry Strategy</h2>
 * <p>The workflow uses an exponential backoff strategy for efficiently managing retries by
 * gradually increasing the wait time between attempts, thereby reducing the load on the
 * external system and improving the chances of a successful interaction.</p>
 *
 * <h2>Usage</h2>
 * <p>To start the Backoff Polling Workflow, use the following REST endpoint:</p>
 * <pre>
 * GET /design-pattern/polling/start/backoff?workflowId={workflowId}
 * </pre>
 * <p>This endpoint initiates the workflow with the specified {@code workflowId}.</p>
 */
public class BackoffPollingWorkflow implements ObjectWorkflow {
    private final List<StateDef> stateDefs;

    public BackoffPollingWorkflow(ServiceDependency service) {
        this.stateDefs = List.of(
                StateDef.startingState(new ReadExternalDepState(service)),
                StateDef.nonStartingState(new PollingCompleteState())
        );
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }
}


/**
 * Represents a state in the Backoff Polling Workflow that attempts to read from an external dependency.
 * <p>This state is responsible for interacting with an external service or system to retrieve necessary data.
 * It employs a retry mechanism with exponential backoff to handle transient failures and ensure robustness.</p>
 *
 * <p>During execution, this state calls the {@link ServiceDependency#attemptExternalApiCall(String)} method
 * to perform the external read operation. If the operation is successful, the workflow transitions to the
 * {@code PollingCompleteState} with the retrieved result.</p>
 */
class ReadExternalDepState implements WorkflowState<Void> {

    private final ServiceDependency service;

    ReadExternalDepState(ServiceDependency service) {
        this.service = service;
    }

    @Override
    public Class<Void> getInputType() {
        return Void.class;
    }

    @Override
    public StateDecision execute(final Context context, final Void input, final CommandResults commandResults, final Persistence persistence, final Communication communication) {
        String result = service.attemptExternalApiCall("Read for BackoffPollingWorkflow");
        return StateDecision.singleNextState(PollingCompleteState.class, result);
    }

    /**
     * By default, all state execution will retry infinitely (until workflow timeout).
     * This may not work for some dependency as we may want to retry for only a certain times
     */
    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setExecuteApiRetryPolicy(
                        new RetryPolicy()
                                .backoffCoefficient(2f)
                                .maximumAttempts(5)
                                .maximumAttemptsDurationSeconds(3600)
                                .initialIntervalSeconds(3)
                                .maximumIntervalSeconds(60)
                );
    }
}


/**
 * Represents the final state in the Backoff Polling Workflow that completes the workflow process.
 * <p>This state is responsible for finalizing the workflow once the external dependency has been successfully accessed
 * and the necessary data has been retrieved.</p>
 *
 * <p>During execution, this state receives the external data input obtained from the previous state
 * and performs any necessary finalization tasks. It then gracefully completes the workflow, ensuring that
 * all resources are properly released and any final notifications or logging are performed.</p>
 */
class PollingCompleteState implements WorkflowState<String> {

    @Override
    public Class<String> getInputType() {
        return String.class;
    }

    @Override
    public StateDecision execute(
            Context context,
            String externalDataInput,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        System.out.printf("Executing final state to complete the workflow: (%s)\n", externalDataInput);

        return StateDecision.gracefulCompleteWorkflow(externalDataInput);
    }
}
