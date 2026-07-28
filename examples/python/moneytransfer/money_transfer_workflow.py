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

from dataclasses import dataclass

from iwf.command_results import CommandResults
from iwf.communication import Communication
from iwf.iwf_api.models import RetryPolicy
from iwf.persistence import Persistence
from iwf.state_decision import StateDecision
from iwf.state_schema import StateSchema
from iwf.workflow import ObjectWorkflow
from iwf.workflow_context import WorkflowContext
from iwf.workflow_state import WorkflowState
from iwf.workflow_state_options import WorkflowStateOptions


@dataclass
class TransferRequest:
    from_account: str
    to_account: str
    amount: int
    notes: str


class VerifyState(WorkflowState[TransferRequest]):
    def execute(
            self,
            ctx: WorkflowContext,
            request: TransferRequest,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        print(f"API to check balance for account {request.from_account} for amount{request.amount}")

        has_sufficient_funds = True
        if not has_sufficient_funds:
            return StateDecision.force_fail_workflow("insufficient funds")

        return StateDecision.single_next_state(CreateDebitMemoState, request)


class CreateDebitMemoState(WorkflowState[TransferRequest]):
    def execute(
            self,
            ctx: WorkflowContext,
            request: TransferRequest,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        print(f"API to create debit memo for account {request.from_account} for amount{request.amount} with notes{request.notes}")
        # uncomment this to test error
        # raise Exception("test error")
        return StateDecision.single_next_state(DebitState, request)

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            proceed_to_state_when_execute_retry_exhausted=CompensateState,
            execute_api_retry_policy=RetryPolicy(
                maximum_attempts_duration_seconds=3600,
                # replace with this to try a shorter retry
                # maximum_attempts_duration_seconds=3,
            )
        )


class DebitState(WorkflowState[TransferRequest]):
    def execute(
            self,
            ctx: WorkflowContext,
            request: TransferRequest,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        print(f"API to debit account {request.from_account} for amount{request.amount}")

        return StateDecision.single_next_state(CreateCreditMemoState, request)

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            proceed_to_state_when_execute_retry_exhausted=CompensateState,
            execute_api_retry_policy=RetryPolicy(
                maximum_attempts_duration_seconds=3600,
            )
        )


class CreateCreditMemoState(WorkflowState[TransferRequest]):
    def execute(
            self,
            ctx: WorkflowContext,
            request: TransferRequest,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        print(f"API to create credit memo for account {request.to_account} for amount{request.amount} with notes{request.notes}")

        return StateDecision.single_next_state(CreditState, request)

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            proceed_to_state_when_execute_retry_exhausted=CompensateState,
            execute_api_retry_policy=RetryPolicy(
                maximum_attempts_duration_seconds=3600,
            )
        )


class CreditState(WorkflowState[TransferRequest]):
    def execute(
            self,
            ctx: WorkflowContext,
            request: TransferRequest,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        print(f"API to credit account {request.to_account} for amount{request.amount}")

        return StateDecision.graceful_complete_workflow(f"transfer is done from account{request.from_account} "
                                                        f"to account{request.to_account} for amount{request.amount}")

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            proceed_to_state_when_execute_retry_exhausted=CompensateState,
            execute_api_retry_policy=RetryPolicy(
                maximum_attempts_duration_seconds=3600,
            )
        )


class CompensateState(WorkflowState[TransferRequest]):
    def execute(
            self,
            ctx: WorkflowContext,
            request: TransferRequest,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        # NOTE: to improve, we can use iWF data attributes to track whether each step has been attempted to execute
        # and check a flag to see if we should undo it or not

        print(f"API to undo credit account {request.to_account} for amount{request.amount}")
        print(f"API to undo create credit memo account {request.to_account} for amount{request.amount}")
        print(f"API to undo debit account {request.from_account} for amount{request.amount}")
        print(f"API to undo create debit memo {request.from_account} for amount{request.amount}")

        return StateDecision.force_fail_workflow("fail to transfer")


class MoneyTransferWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(
            VerifyState(),
            CreateDebitMemoState(),
            DebitState(),
            CreateCreditMemoState(),
            CreditState(),
            CompensateState())
