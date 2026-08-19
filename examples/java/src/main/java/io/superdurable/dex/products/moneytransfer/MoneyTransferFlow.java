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

package io.superdurable.dex.products.moneytransfer;

import io.superdurable.dex.Context;
import io.superdurable.dex.Flow;
import io.superdurable.dex.PersistenceSchema;
import io.superdurable.dex.RetryPolicy;
import io.superdurable.dex.Step;
import io.superdurable.dex.StepDecision;
import io.superdurable.dex.StepList;
import io.superdurable.dex.StepOptions;
import io.superdurable.dex.shared.MyDependencyService;
import org.springframework.stereotype.Component;

import java.time.Duration;

@Component
public class MoneyTransferFlow implements Flow<TransferRequest> {
    private final MyDependencyService service;
    private final CheckBalance checkBalance = new CheckBalance();
    private final CreateDebitMemo createDebitMemo = new CreateDebitMemo();
    private final Debit debit = new Debit();
    private final CreateCreditMemo createCreditMemo = new CreateCreditMemo();
    private final Credit credit = new Credit();
    private final Compensate compensate = new Compensate();

    public MoneyTransferFlow(final MyDependencyService service) {
        this.service = service;
    }

    @Override
    public StepList<TransferRequest> getSteps() {
        return StepList.startStep(checkBalance)
                .otherSteps(createDebitMemo, debit, createCreditMemo, credit, compensate);
    }

    @Override
    public PersistenceSchema getPersistenceSchema() {
        return PersistenceSchema.of();
    }

    private StepOptions compensatedStepOptions(final Duration totalDuration) {
        return StepOptions.newBuilder()
                .executeRetry(RetryPolicy.newBuilder().totalDuration(totalDuration).build())
                .onExecuteFailureProceedTo(
                        compensate,
                        StepOptions.newBuilder()
                                .executeRetry(RetryPolicy.newBuilder()
                                        .totalDuration(Duration.ofHours(24))
                                        .build())
                                .build())
                .build();
    }

    final class CheckBalance implements Step<TransferRequest> {
        @Override
        public Class<TransferRequest> getInputType() {
            return TransferRequest.class;
        }

        @Override
        public StepDecision execute(final Context context, final TransferRequest request) {
            if (!service.checkBalance(request.fromAccount, request.amount)) {
                return StepDecision.forceFail("insufficient funds");
            }
            return StepDecision.goTo(createDebitMemo, request);
        }
    }

    final class CreateDebitMemo implements Step<TransferRequest> {
        @Override
        public Class<TransferRequest> getInputType() {
            return TransferRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return compensatedStepOptions(Duration.ofHours(1));
        }

        @Override
        public StepDecision execute(final Context context, final TransferRequest request) {
            service.createDebitMemo(request.fromAccount, request.amount, request.notes);
            return StepDecision.goTo(debit, request);
        }
    }

    final class Debit implements Step<TransferRequest> {
        @Override
        public Class<TransferRequest> getInputType() {
            return TransferRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return compensatedStepOptions(Duration.ofHours(1));
        }

        @Override
        public StepDecision execute(final Context context, final TransferRequest request) {
            service.debit(request.fromAccount, request.amount);
            return StepDecision.goTo(createCreditMemo, request);
        }
    }

    final class CreateCreditMemo implements Step<TransferRequest> {
        @Override
        public Class<TransferRequest> getInputType() {
            return TransferRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return compensatedStepOptions(Duration.ofHours(1));
        }

        @Override
        public StepDecision execute(final Context context, final TransferRequest request) {
            service.createCreditMemo(request.toAccount, request.amount, request.notes);
            return StepDecision.goTo(credit, request);
        }
    }

    final class Credit implements Step<TransferRequest> {
        @Override
        public Class<TransferRequest> getInputType() {
            return TransferRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return compensatedStepOptions(Duration.ofHours(1));
        }

        @Override
        public StepDecision execute(final Context context, final TransferRequest request) {
            service.credit(request.toAccount, request.amount);
            return StepDecision.gracefulComplete(String.format(
                    "transfer is done from %s to %s for amount %d",
                    request.fromAccount,
                    request.toAccount,
                    request.amount));
        }
    }

    final class Compensate implements Step<TransferRequest> {
        @Override
        public Class<TransferRequest> getInputType() {
            return TransferRequest.class;
        }

        @Override
        public StepOptions getStepOptions() {
            return StepOptions.newBuilder()
                    .executeRetry(RetryPolicy.newBuilder()
                            .totalDuration(Duration.ofHours(24))
                            .build())
                    .build();
        }

        @Override
        public StepDecision execute(final Context context, final TransferRequest request) {
            service.undoCredit(request.toAccount, request.amount);
            service.undoCreateCreditMemo(request.toAccount, request.amount, request.notes);
            service.undoCreateDebitMemo(request.fromAccount, request.amount, request.notes);
            service.undoDebit(request.fromAccount, request.amount);
            return StepDecision.forceFail(String.format(
                    "transfer has failed from %s to %s for amount %d",
                    request.fromAccount,
                    request.toAccount,
                    request.amount));
        }
    }
}
