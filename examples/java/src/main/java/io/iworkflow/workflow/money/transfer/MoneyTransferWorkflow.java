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

package io.iworkflow.workflow.money.transfer;

import io.iworkflow.core.Context;
import io.iworkflow.core.ObjectWorkflow;
import io.iworkflow.core.StateDecision;
import io.iworkflow.core.StateDef;
import io.iworkflow.core.WorkflowState;
import io.iworkflow.core.command.CommandResults;
import io.iworkflow.core.communication.Communication;
import io.iworkflow.core.persistence.Persistence;
import io.iworkflow.gen.models.RetryPolicy;
import io.iworkflow.core.WorkflowStateOptions;
import io.iworkflow.workflow.MyDependencyService;
import org.springframework.stereotype.Component;

import java.util.Arrays;
import java.util.List;

@Component

public class MoneyTransferWorkflow implements ObjectWorkflow {
    private MyDependencyService myService;

    public MoneyTransferWorkflow(MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public List<StateDef> getWorkflowStates() {
        return Arrays.asList(
                StateDef.startingState(new VerifyState(myService)),
                StateDef.nonStartingState(new CreateDebitMemoState(myService)),
                StateDef.nonStartingState(new DebitState(myService)),
                StateDef.nonStartingState(new CreateCreditMemoState(myService)),
                StateDef.nonStartingState(new CreditState(myService)),
                StateDef.nonStartingState(new CompensateState(myService)));
    }
}

class VerifyState implements WorkflowState<TransferRequest> {

    private MyDependencyService myService;

    public VerifyState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<TransferRequest> getInputType() {
        return TransferRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final TransferRequest request,
            final CommandResults commandResults, Persistence persistence, final Communication communication) {
        boolean hasSufficientFunds = myService.checkBalance(request.getFromAccountId(), request.getAmount());
        if (!hasSufficientFunds) {
            StateDecision.forceCompleteWorkflow("insufficient funds");
        }
        return StateDecision.singleNextState(CreateDebitMemoState.class, request);
    }
}

class CreateDebitMemoState implements WorkflowState<TransferRequest> {
    private MyDependencyService myService;

    public CreateDebitMemoState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<TransferRequest> getInputType() {
        return TransferRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final TransferRequest request,
            final CommandResults commandResults, Persistence persistence, final Communication communication) {
        myService.createDebitMemo(request.getFromAccountId(), request.getAmount(), request.getNotes());
        // uncomment to test the error case
        // if (true) {
        // throw new RuntimeException("test error case");
        // }
        return StateDecision.singleNextState(DebitState.class, request);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(CompensateState.class)
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttemptsDurationSeconds(3600));
        // NOTE: for demo purposes, setting this to 3 seconds
        // .maximumAttemptsDurationSeconds(3));
    }
}

class DebitState implements WorkflowState<TransferRequest> {
    private MyDependencyService myService;

    public DebitState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<TransferRequest> getInputType() {
        return TransferRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final TransferRequest request,
            final CommandResults commandResults, Persistence persistence, final Communication communication) {
        myService.debit(request.getFromAccountId(), request.getAmount());
        return StateDecision.singleNextState(CreateCreditMemoState.class, request);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(CompensateState.class)
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttemptsDurationSeconds(3600));
        // NOTE: for demo purposes, setting this to 3 seconds
        // .maximumAttemptsDurationSeconds(3));
    }
}

class CreateCreditMemoState implements WorkflowState<TransferRequest> {
    private MyDependencyService myService;

    public CreateCreditMemoState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<TransferRequest> getInputType() {
        return TransferRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final TransferRequest request,
            final CommandResults commandResults, Persistence persistence, final Communication communication) {
        myService.createCreditMemo(request.getToAccountId(), request.getAmount(), request.getNotes());
        return StateDecision.singleNextState(CreditState.class, request);
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(CompensateState.class)
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttemptsDurationSeconds(3600));
        // NOTE: for demo purposes, setting this to 3 seconds
        // .maximumAttemptsDurationSeconds(3));
    }
}

class CreditState implements WorkflowState<TransferRequest> {
    private MyDependencyService myService;

    public CreditState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<TransferRequest> getInputType() {
        return TransferRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final TransferRequest request,
            final CommandResults commandResults, Persistence persistence, final Communication communication) {
        myService.credit(request.getToAccountId(), request.getAmount());
        return StateDecision.gracefulCompleteWorkflow(
                String.format("transfer is done %d from %s to %s", request.getAmount(), request.getFromAccountId(),
                        request.getToAccountId()));
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setProceedToStateWhenExecuteRetryExhausted(CompensateState.class)
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttemptsDurationSeconds(3600));
        // NOTE: for demo purposes, setting this to 3 seconds
        // .maximumAttemptsDurationSeconds(3));
    }
}

class CompensateState implements WorkflowState<TransferRequest> {
    private MyDependencyService myService;

    public CompensateState(final MyDependencyService myService) {
        this.myService = myService;
    }

    @Override
    public Class<TransferRequest> getInputType() {
        return TransferRequest.class;
    }

    @Override
    public StateDecision execute(final Context context, final TransferRequest request,
            final CommandResults commandResults, Persistence persistence, final Communication communication) {
        // NOTE: to improve, we can use iWF data attributes to track whether each step
        // has been attempted to execute
        // and check a flag to see if we should undo it or not

        myService.undoCredit(request.getToAccountId(), request.getAmount());
        myService.undoCreateCreditMemo(request.getToAccountId(), request.getAmount(), request.getNotes());
        myService.undoDebit(request.getFromAccountId(), request.getAmount());
        myService.undoCreateDebitMemo(request.getFromAccountId(), request.getAmount(), request.getNotes());

        return StateDecision.forceFailWorkflow("failed to transfer money");
    }

    @Override
    public WorkflowStateOptions getStateOptions() {
        return new WorkflowStateOptions()
                .setExecuteApiRetryPolicy(new RetryPolicy()
                        .maximumAttemptsDurationSeconds(86400));
    }
}
