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

from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Attribute,
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    go_to,
    graceful_complete,
)

from dex_examples.patterns.fail_fast.payment_provider import (
    FakePaymentProvider,
    PayoutRejectedError,
)


@dataclass
class ExpenseClaim:
    amount_cents: int
    bank_account: str


@dataclass
class ClaimRejection:
    reason: str
    submit_attempt: int


@dataclass
class PayoutOutcome:
    status: str
    detail: str
    submit_attempt: int


PAYOUT_OUTCOME = Attribute("PayoutOutcome", PayoutOutcome)


# dex:group group-id:rejection group-label:"Rejection"
# dex:explanation text:"Record the rejected claim and its reason."
class RecordRejectedClaim(Step[ClaimRejection]):
    def execute(self, context: Context, input: ClaimRejection) -> StepDecision:
        PAYOUT_OUTCOME.set(
            context, PayoutOutcome("REJECTED", input.reason, input.submit_attempt)
        )
        return graceful_complete(f"rejected: {input.reason}")


# dex:group group-id:payout group-label:"Payout"
# dex:explanation text:"Submit the payout; retry a timeout and route a rejection at once."
class SubmitPayout(Step[ExpenseClaim]):
    def __init__(self, provider: FakePaymentProvider) -> None:
        self.provider = provider

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                backoff_coefficient=2.0,
                maximum_interval=timedelta(minutes=5),
                total_duration=timedelta(hours=1),
            )
        )

    def execute(self, context: Context, input: ExpenseClaim) -> StepDecision:
        try:
            payout_id = self.provider.submit_payout(context.flow_id, input)
        except PayoutRejectedError as rejection:
            return go_to(
                RecordRejectedClaim, ClaimRejection(str(rejection), context.attempt)
            )
        PAYOUT_OUTCOME.set(context, PayoutOutcome("PAID", payout_id, context.attempt))
        return graceful_complete(payout_id)


class ExpenseReimbursementFlow(Flow[ExpenseClaim]):
    def __init__(self, provider: FakePaymentProvider) -> None:
        self.submit_payout = SubmitPayout(provider)
        self.record_rejected_claim = RecordRejectedClaim()

    def get_steps(self) -> StepList[ExpenseClaim]:
        return StepList.start_step(self.submit_payout).other_steps(
            self.record_rejected_claim
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(PAYOUT_OUTCOME)
