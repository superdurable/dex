# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from datetime import timedelta

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    force_fail,
    go_to,
    graceful_complete,
)

from dex_examples.my_dependency_service import MyDependencyService
from dex_examples.workflow.money.transfer.transfer_request import TransferRequest

COMPENSATE_RETRY = RetryPolicy(total_duration=timedelta(hours=24))


class Compensate(Step[TransferRequest]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=COMPENSATE_RETRY)

    def execute(self, context: Context, input: TransferRequest) -> StepDecision:
        del context
        self.service.undo_credit(input.to_account, input.amount)
        self.service.undo_create_credit_memo(
            input.to_account,
            input.amount,
            input.notes,
        )
        self.service.undo_create_debit_memo(
            input.from_account,
            input.amount,
            input.notes,
        )
        self.service.undo_debit(input.from_account, input.amount)
        return force_fail(
            f"transfer has failed from {input.from_account} "
            f"to {input.to_account} for amount {input.amount}"
        )


def compensated_step_options(
    compensate: Compensate,
    total_duration: timedelta,
) -> StepOptions:
    return StepOptions(
        execute_retry=RetryPolicy(total_duration=total_duration)
    ).on_execute_failure_proceed_to(
        compensate,
        StepOptions(execute_retry=COMPENSATE_RETRY),
    )


class Credit(Step[TransferRequest]):
    def __init__(self, service: MyDependencyService, options: StepOptions) -> None:
        self.service = service
        self.options = options

    def get_step_options(self) -> StepOptions:
        return self.options

    def execute(self, context: Context, input: TransferRequest) -> StepDecision:
        del context
        self.service.credit(input.to_account, input.amount)
        return graceful_complete(
            f"transfer is done from {input.from_account} "
            f"to {input.to_account} for amount {input.amount}"
        )


class CreateCreditMemo(Step[TransferRequest]):
    def __init__(
        self,
        service: MyDependencyService,
        credit: Credit,
        options: StepOptions,
    ) -> None:
        self.service = service
        self.credit = credit
        self.options = options

    def get_step_options(self) -> StepOptions:
        return self.options

    def execute(self, context: Context, input: TransferRequest) -> StepDecision:
        del context
        self.service.create_credit_memo(input.to_account, input.amount, input.notes)
        return go_to(self.credit, input)


class Debit(Step[TransferRequest]):
    def __init__(
        self,
        service: MyDependencyService,
        create_credit_memo: CreateCreditMemo,
        options: StepOptions,
    ) -> None:
        self.service = service
        self.create_credit_memo = create_credit_memo
        self.options = options

    def get_step_options(self) -> StepOptions:
        return self.options

    def execute(self, context: Context, input: TransferRequest) -> StepDecision:
        del context
        self.service.debit(input.from_account, input.amount)
        return go_to(self.create_credit_memo, input)


class CreateDebitMemo(Step[TransferRequest]):
    def __init__(
        self,
        service: MyDependencyService,
        debit: Debit,
        options: StepOptions,
    ) -> None:
        self.service = service
        self.debit = debit
        self.options = options

    def get_step_options(self) -> StepOptions:
        return self.options

    def execute(self, context: Context, input: TransferRequest) -> StepDecision:
        del context
        self.service.create_debit_memo(input.from_account, input.amount, input.notes)
        return go_to(self.debit, input)


class CheckBalance(Step[TransferRequest]):
    def __init__(
        self,
        service: MyDependencyService,
        create_debit_memo: CreateDebitMemo,
    ) -> None:
        self.service = service
        self.create_debit_memo = create_debit_memo

    def execute(self, context: Context, input: TransferRequest) -> StepDecision:
        del context
        if not self.service.check_balance(input.from_account, input.amount):
            return force_fail("insufficient funds")
        return go_to(self.create_debit_memo, input)


class MoneyTransferFlow(Flow[TransferRequest]):
    def __init__(self, service: MyDependencyService) -> None:
        self.service = service
        self.compensate = Compensate(service)
        options = compensated_step_options(self.compensate, timedelta(hours=1))
        self.credit = Credit(service, options)
        self.create_credit_memo = CreateCreditMemo(service, self.credit, options)
        self.debit = Debit(service, self.create_credit_memo, options)
        self.create_debit_memo = CreateDebitMemo(service, self.debit, options)
        self.check_balance = CheckBalance(service, self.create_debit_memo)

    def get_steps(self) -> StepList[TransferRequest]:
        return StepList.start_step(self.check_balance).other_steps(
            self.create_debit_memo,
            self.debit,
            self.create_credit_memo,
            self.credit,
            self.compensate,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
