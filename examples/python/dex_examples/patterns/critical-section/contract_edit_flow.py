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

from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass, field, replace
from datetime import timedelta
from typing import Any

from dex import (
    AsyncContext,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    graceful_complete,
)

CONTRACT_EDIT_SECONDS = 0.1


@dataclass(frozen=True)
class ContractEdit:
    """One reviewer's turn to edit the shared contract.

    The should_* fields simulate a failure or a withdrawal for the example.
    """

    reviewer: str
    should_fail_handoff: bool = field(
        default=False, metadata={"dex_json_name": "shouldFailHandoff"}
    )
    should_withdraw: bool = field(
        default=False, metadata={"dex_json_name": "shouldWithdraw"}
    )
    should_fail_edit: bool = field(
        default=False, metadata={"dex_json_name": "shouldFailEdit"}
    )
    started_at_millis: int = field(
        default=0, metadata={"dex_json_name": "startedAtMillis"}
    )


@dataclass(frozen=True)
class ContractEditRecord:
    """When one edit held the contract and how it ended."""

    reviewer: str
    outcome: str
    started_at_millis: int = field(metadata={"dex_json_name": "startedAtMillis"})
    finished_at_millis: int = field(metadata={"dex_json_name": "finishedAtMillis"})


class ContractEditError(Exception):
    pass


CONTRACT_EDIT_TOKEN = Channel("contractEditToken", str)
CONTRACT_EDIT_FINISHED = Channel("contractEditFinished", ContractEditRecord)
CONTRACT_EDIT_RETRY = RetryPolicy(
    initial_interval=timedelta(seconds=1), maximum_attempts=2
)


class StartContractEdits(Step[list[ContractEdit]]):
    """Publishes the only token and starts one branch per reviewer."""

    def execute(self, context: Context, edits: list[ContractEdit]) -> StepDecision:
        CONTRACT_EDIT_TOKEN.publish(context, "contract")
        movements: list[StepMovement[Any]] = [
            StepMovement.of(EnterContractEdit, edit) for edit in edits
        ]
        movements.append(StepMovement.of(FinishContractEdits, len(edits)))
        return go_to_many(*movements)


class EnterContractEdit(Step[ContractEdit]):
    """Takes the token and hands off; it does no protected work, so canceling it is safe."""

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=CONTRACT_EDIT_RETRY).on_execute_failure_proceed_to(
            ReleaseContractEditToken
        )

    def wait_for(self, context: Context, edit: ContractEdit) -> Wait:
        return Wait.until(CONTRACT_EDIT_TOKEN.for_one())

    def execute(self, context: Context, edit: ContractEdit) -> StepDecision:
        if edit.should_fail_handoff:
            raise ContractEditError("contract handoff failed")
        return go_to(EditContract, edit)


class EditContract(Step[ContractEdit]):
    """The first Step that works on the contract. Never cancel it."""

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=CONTRACT_EDIT_RETRY).on_execute_failure_proceed_to(
            ReleaseContractEditToken
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, edit: ContractEdit
    ) -> StepDecision:
        started_edit = replace(edit, started_at_millis=current_time_millis())
        if started_edit.should_withdraw:
            CONTRACT_EDIT_FINISHED.publish(
                context, finish_contract_edit(started_edit, "withdrawn")
            )
            CONTRACT_EDIT_TOKEN.publish(context, "contract")
            return dead_end()
        await asyncio.sleep(CONTRACT_EDIT_SECONDS)
        return go_to(SaveContractEdit, started_edit)


class SaveContractEdit(Step[ContractEdit]):
    """The last Step of the section; it records the edit and publishes the token back."""

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_retry=CONTRACT_EDIT_RETRY).on_execute_failure_proceed_to(
            ReleaseContractEditToken
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, edit: ContractEdit
    ) -> StepDecision:
        await asyncio.sleep(CONTRACT_EDIT_SECONDS)
        if edit.should_fail_edit:
            raise ContractEditError("contract edit failed")
        CONTRACT_EDIT_FINISHED.publish(context, finish_contract_edit(edit, "saved"))
        CONTRACT_EDIT_TOKEN.publish(context, "contract")
        return dead_end()


class ReleaseContractEditToken(Step[ContractEdit]):
    """The failure exit of the section."""

    def execute(self, context: Context, edit: ContractEdit) -> StepDecision:
        CONTRACT_EDIT_FINISHED.publish(context, finish_contract_edit(edit, "failed"))
        CONTRACT_EDIT_TOKEN.publish(context, "contract")
        return dead_end()


class FinishContractEdits(Step[int]):
    """Joins the branches after every reviewer leaves the section."""

    def wait_for(self, context: Context, edit_count: int) -> Wait:
        return Wait.until(CONTRACT_EDIT_FINISHED.for_n(edit_count))

    def execute(self, context: Context, edit_count: int) -> StepDecision:
        return graceful_complete(list(CONTRACT_EDIT_FINISHED.results(context)))


def finish_contract_edit(edit: ContractEdit, outcome: str) -> ContractEditRecord:
    """Records the edit; one that never started has an empty interval at its finish time."""
    finished_at_millis = current_time_millis()
    return ContractEditRecord(
        reviewer=edit.reviewer,
        outcome=outcome,
        started_at_millis=edit.started_at_millis or finished_at_millis,
        finished_at_millis=finished_at_millis,
    )


def current_time_millis() -> int:
    return time.time_ns() // 1_000_000


class ContractEditFlow(Flow[list[ContractEdit]]):
    """Lets several reviewers edit one contract, one at a time.

    A one-message Channel is the token that guards the edit section.
    """

    def __init__(self) -> None:
        self.start_contract_edits = StartContractEdits()
        self.enter_contract_edit = EnterContractEdit()
        self.edit_contract = EditContract()
        self.save_contract_edit = SaveContractEdit()
        self.release_contract_edit_token = ReleaseContractEditToken()
        self.finish_contract_edits = FinishContractEdits()

    def get_steps(self) -> StepList[list[ContractEdit]]:
        return StepList.start_step(self.start_contract_edits).other_steps(
            self.enter_contract_edit,
            self.edit_contract,
            self.save_contract_edit,
            self.release_contract_edit_token,
            self.finish_contract_edits,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(CONTRACT_EDIT_TOKEN, CONTRACT_EDIT_FINISHED)
