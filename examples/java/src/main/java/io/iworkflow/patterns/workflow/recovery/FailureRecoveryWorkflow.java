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

package io.iworkflow.patterns.workflow.recovery;

import static io.iworkflow.patterns.workflow.recovery.FailureRecoveryWorkflow.WORKFLOW_INPUT_KEY;
import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.WorkflowStateOptions;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.DataAttributeDef;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.core.persistence.PersistenceFieldDef;
import io.iworkflow.gen.models.RetryPolicy;

import java.util.List;
import java.util.Random;

public class FailureRecoveryWorkflow implements ObjectWorkflow {
    public static final String WORKFLOW_INPUT_KEY = "workflow-input-data-attribute-key";

    private final List<StateDef> stateDefs;

    public FailureRecoveryWorkflow() {
        final DatabaseConnection db = new DatabaseConnection();
        final PaymentProcessor paymentProcessor = new PaymentProcessor();

        this.stateDefs = List.of(
                StateDef.startingState(new UpdateItemQuantityState(db)),
                StateDef.nonStartingState(new ChargeForItemsState(db, paymentProcessor)),
                StateDef.nonStartingState(new UpdateQuantityRecoveryState(db)),
                StateDef.nonStartingState(new VoidPaymentRecoveryState(db, paymentProcessor)));
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return stateDefs;
    }

    @Override
    public List<PersistenceFieldDef> getPersistenceSchema() {
        return List.of(
                DataAttributeDef.create(FailureRecoveryWorkflowInput.class, WORKFLOW_INPUT_KEY));
    }
}

class UpdateItemQuantityState implements WorkflowState<FailureRecoveryWorkflowInput> {
    private final DatabaseConnection database;

    UpdateItemQuantityState(DatabaseConnection database) {
        this.database = database;
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        // The `WorkflowStateOptions` gives greater control over how the state will behave. If a state will fail indefinitely,
        // the only way to recover from that state is to set the retry policy to tell iWF how to handle a state that will not
        // succeed.
        //
        // The main controls used for workflow recovery are the `ProceedToStateWhenExecuteRetryExhausted` and
        // `ExecuteApiRetryPolicy`. `ProceedToStateWhenExecuteRetryExhausted` tells iWF which state to move to when the current
        // state has failed. `ExecuteApiRetryPolicy` tells iWF how long or how many times a state is able to continue to
        // retrying when it has failed.
        return new WorkflowStateOptions()
                // `ProceedToStateWhenExecuteRetryExhausted` controls the state that the workflow will transition to if this
                // state should fail more times than allowed by the configuration. By default, a state can retry until the
                // workflow timeout has passed. To ensure that a state can fail and move to a recovery state, there needs to be
                // a retry policy that tells iWF when to allow the state to fail.
                .setProceedToStateWhenExecuteRetryExhausted(UpdateQuantityRecoveryState.class)
                // `ExecuteApiRetryPolicy` tells iWF for how long or how many times a state is allowed to fail and retry. Here,
                // a new retry policy is set with its maximum number of attempts set to 1, so this state will only ever run
                // once. If `MaximumAttempts` is set to 0, the state can retry as many times as can fit within the workflow
                // timeout. The retry policy also includes `MaximumAttemptsDurationSeconds`, which will allow the state to retry
                // as many times as can fit in the number of seconds set. Like `MaximumAttempts`, if
                // `MaximumAttemptsDurationSeconds` is set to 0, the state can retry until the workflow timeout is reached. If
                // both `MaximumAttempts` and `MaximumAttemptsDurationSeconds` are set, whichever threshold is reached first
                // will determine when the state has finished retrying.
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttempts(5));
    }

    @Override
    public Class<FailureRecoveryWorkflowInput> getInputType() {
        return FailureRecoveryWorkflowInput.class;
    }

    @Override
    public StateDecision execute(
            Context context,
            FailureRecoveryWorkflowInput input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        persistence.setDataAttribute(FailureRecoveryWorkflow.WORKFLOW_INPUT_KEY, input);

        database.reduceQuantity(input.getItemName(), input.getRequestedQuantity());

        return StateDecision.singleNextState(ChargeForItemsState.class, input.getRequestedQuantity());
    }
}

class ChargeForItemsState implements WorkflowState<Integer> {
    private final DatabaseConnection databaseConnection;
    private final PaymentProcessor paymentProcessor;

    ChargeForItemsState(DatabaseConnection databaseConnection, PaymentProcessor paymentProcessor) {
        this.databaseConnection = databaseConnection;
        this.paymentProcessor = paymentProcessor;
    }

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(VoidPaymentRecoveryState.class)
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttempts(5));
    }

    @Override
    public StateDecision execute(
            Context context,
            Integer quantityRequested,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        final FailureRecoveryWorkflowInput workflowInput = persistence.getDataAttribute(
                FailureRecoveryWorkflow.WORKFLOW_INPUT_KEY, FailureRecoveryWorkflowInput.class);

        final double itemValue = databaseConnection.getItemPrice(workflowInput.getItemName());
        final double orderValue = workflowInput.getRequestedQuantity() * itemValue;
        paymentProcessor.processPayment(orderValue);

        return StateDecision.gracefulCompleteWorkflow();
    }
}

class UpdateQuantityRecoveryState implements WorkflowState<FailureRecoveryWorkflowInput> {
    private final DatabaseConnection database;

    UpdateQuantityRecoveryState(DatabaseConnection database) {
        this.database = database;
    }

    @Override
    public Class<FailureRecoveryWorkflowInput> getInputType() {
        return FailureRecoveryWorkflowInput.class;
    }

    @Override
    public StateDecision execute(
            Context context,
            FailureRecoveryWorkflowInput input,
            CommandResults commandResults,
            Persistence persistence,
            Communication communication) {
        database.increaseQuantity(input.getItemName(), input.getRequestedQuantity());

        return StateDecision.forceFailWorkflow("Failed to process transaction");
    }
}

class VoidPaymentRecoveryState implements WorkflowState<Integer> {
    private final DatabaseConnection database;
    private final PaymentProcessor paymentProcessor;

    VoidPaymentRecoveryState(DatabaseConnection database, PaymentProcessor paymentProcessor) {
        this.database = database;
        this.paymentProcessor = paymentProcessor;
    }

    @Override
    public Class<Integer> getInputType() {
        return Integer.class;
    }

    @Override
    public StateDecision execute(Context context, Integer input, CommandResults commandResults, Persistence persistence, Communication communication) {
        final FailureRecoveryWorkflowInput workflowInput = persistence.getDataAttribute(
                FailureRecoveryWorkflow.WORKFLOW_INPUT_KEY, FailureRecoveryWorkflowInput.class);

        final double itemValue = database.getItemPrice(workflowInput.getItemName());
        final double orderValue = workflowInput.getRequestedQuantity() * itemValue;
        paymentProcessor.voidPayment(orderValue);

        return StateDecision.singleNextState(UpdateQuantityRecoveryState.class, workflowInput);
    }
}

class DatabaseConnection {
    private static final Random RANDOM = new Random();

    public void reduceQuantity(final String itemName, final int quantity) {
        System.out.println("Reducing quantity: " + quantity);
        if (quantity > RANDOM.nextInt(10)) {
            throw new RuntimeException("not enough items available");
        }
    }

    public void increaseQuantity(final String itemName, final int quantity) {
        System.out.println("Increasing quantity: " + quantity);
    }

    public double getItemPrice(final String itemName) {
        return 3.14;
    }
}

class PaymentProcessor {
    public void processPayment(final double price) {
        throw new RuntimeException("Payment could not be processed");
    }

    public void voidPayment(final double price) {
        System.out.printf("Voiding payment for $ %.2f%n", price);
    }
}
