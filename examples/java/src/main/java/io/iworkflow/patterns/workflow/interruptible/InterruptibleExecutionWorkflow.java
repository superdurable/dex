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

package io.iworkflow.patterns.workflow.interruptible;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.RPC;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.StateMovement;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;

import java.util.Arrays;
import java.util.List;

/**
 * The InterruptibleExecutionWorkflow class implements an interruptible workflow using the iWorkflow framework.
 * It defines a workflow with multiple states that can be interrupted via an RPC call.
 */
public class InterruptibleExecutionWorkflow implements ObjectWorkflow {
    private final List<StateDef> stateDefs;
    public static final String DA_INTERRUPT_SIGNAL = "interruptSignal";

    /**
     * Constructs an InterruptibleExecutionWorkflow with predefined states.
     * The workflow consists of an initial state and two execution states.
     */
    public InterruptibleExecutionWorkflow() {
        this.stateDefs = Arrays.asList(
                StateDef.startingState(new InitState()),
                StateDef.nonStartingState(new WorkAExecutionState()),
                StateDef.nonStartingState(new WorkNExecutionState())
        );
    }

    /**
     * Returns the list of states defined in the workflow.
     *
     * @return a list of StateDef objects representing the workflow states.
     */
    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    /**
     * Returns the persistence schema for the workflow.
     * This schema includes a data attribute for interrupt signals.
     *
     * @return a list of PersistenceFieldDef objects representing the persistence schema.
     */
    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return Arrays.asList(
                DataAttributeDef.create(String.class, DA_INTERRUPT_SIGNAL)
        );
    }

    /**
     * RPC method to interrupt the workflow execution.
     * Sets the interrupt signal in the persistence layer to "cancel".
     *
     * @param context the workflow context.
     * @param persistence the persistence interface for managing workflow data.
     * @param communication the communication interface for workflow interactions.
     */
    @RPC
    public void interrupt(Context context, Persistence persistence, Communication communication) {
        persistence.setDataAttribute(DA_INTERRUPT_SIGNAL, "cancel");
    }
}

/**
 * The InitState class represents the initial state of the workflow.
 * It initializes the workflow by setting up the parameters for subsequent states and starting them.
 */
class InitState implements WorkflowState<Void> {

    @Override
    public Class<Void> getInputType() {
        return null;
    }

    @Override
    public StateDecision execute(
            Context context,
            Void unused,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {

        WorkJobParametersInput input = ImmutableWorkJobParametersInput.builder().jobUpperBound(15).build();

        return StateDecision.multiNextStates(
                StateMovement.create(WorkAExecutionState.class, input),
                StateMovement.create(WorkNExecutionState.class, input)
        );
    }
}

/**
 * The WorkAExecutionState class represents a state in the workflow that performs a specific task.
 * It can be interrupted based on the interrupt signal.
 */
class WorkAExecutionState implements WorkflowState<WorkJobParametersInput> {

    @Override
    public Class<WorkJobParametersInput> getInputType() {
        return WorkJobParametersInput.class;
    }

    /**
     * Executes the state logic, processes the job, and checks for interruptions.
     *
     * @param context the workflow context.
     * @param input the input data for the state.
     * @param commandResults the results of any commands executed.
     * @param persistence the persistence interface for managing workflow data.
     * @param communication the communication interface for workflow interactions.
     * @return a StateDecision object representing the next state or completion.
     */
    @Override
    public StateDecision execute(
            Context context,
            WorkJobParametersInput input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        String interruptSignal = persistence.getDataAttribute(InterruptibleExecutionWorkflow.DA_INTERRUPT_SIGNAL, String.class);
        if(interruptSignal != null && interruptSignal.equals("cancel")) {
            System.out.println("A: Interrupted!");
            return StateDecision.gracefulCompleteWorkflow();
        }

        if(input.getProgress() > input.getJobUpperBound()) {
            System.out.println("Executing WorkAExecutionState completed");
            return StateDecision.gracefulCompleteWorkflow();
        }
        processJob(context.getWorkflowId(), context.getStateExecutionId().get(), input.getProgress());

        var inputWithProgress = ImmutableWorkJobParametersInput.builder().jobUpperBound(input.getJobUpperBound()).progress(input.getProgress() + 1).build();
        return StateDecision.singleNextState(WorkAExecutionState.class, inputWithProgress);
    }

    /**
     * Processes a job and simulates work by sleeping for a specified duration.
     */
    private void processJob(String workflowId, String stateExecutionId, int jobNumber) {
        System.out.printf("[%s][%s]: Doing job %s\n", workflowId, stateExecutionId, jobNumber);

        try {
            Thread.sleep(1500);
        } catch (InterruptedException e) {
            throw new RuntimeException(e);
        }
    }
}

/**
 * The WorkNExecutionState class represents another state in the workflow that performs a specific task.
 * It can also be interrupted based on the interrupt signal.
 */
class WorkNExecutionState implements WorkflowState<WorkJobParametersInput> {

    @Override
    public Class<WorkJobParametersInput> getInputType() {
        return WorkJobParametersInput.class;
    }

    /**
     * Executes the state logic, processes the job, and checks for interruptions.
     *
     * @param context the workflow context.
     * @param input the input data for the state.
     * @param commandResults the results of any commands executed.
     * @param persistence the persistence interface for managing workflow data.
     * @param communication the communication interface for workflow interactions.
     * @return a StateDecision object representing the next state or completion.
     */
    @Override
    public StateDecision execute(
            Context context,
            WorkJobParametersInput input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        String interruptSignal = persistence.getDataAttribute(InterruptibleExecutionWorkflow.DA_INTERRUPT_SIGNAL, String.class);
        if(interruptSignal != null && interruptSignal.equals("cancel")) {
            System.out.println("N: Interrupted!");
            return StateDecision.gracefulCompleteWorkflow();
        }

        if(input.getProgress() > input.getJobUpperBound()) {
            System.out.println("Executing WorkNExecutionState completed");
            return StateDecision.gracefulCompleteWorkflow();
        }

        processJob(context.getWorkflowId(), context.getStateExecutionId().get(), input.getProgress());

        var inputWithProgress = ImmutableWorkJobParametersInput.builder().jobUpperBound(input.getJobUpperBound()).progress(input.getProgress() + 1).build();
        return StateDecision.singleNextState(WorkNExecutionState.class, inputWithProgress);
    }

    /**
     * Processes a job and simulates work by sleeping for a specified duration.
     */
    private void processJob(String workflowId, String stateExecutionId, int jobNumber) {
        System.out.printf("[%s][%s]: Processing job %s\n", workflowId, stateExecutionId, jobNumber);

        try {
            Thread.sleep(3000);
        } catch (InterruptedException e) {
            throw new RuntimeException(e);
        }
    }
}

