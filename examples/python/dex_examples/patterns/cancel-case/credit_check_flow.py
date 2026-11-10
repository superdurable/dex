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

from dataclasses import dataclass, field
from datetime import timedelta

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    graceful_complete,
)


@dataclass(frozen=True)
class CreditCheckRequest:
    """The input of one credit check child Flow."""

    applicant_id: str = field(metadata={"dex_json_name": "applicantId"})
    # Simulates how long the credit bureau takes to answer.
    report_delay_seconds: int = field(metadata={"dex_json_name": "reportDelaySeconds"})


class AwaitCreditReport(Step[CreditCheckRequest]):
    """Waits for the simulated credit bureau to answer."""

    def wait_for(self, context: Context, request: CreditCheckRequest) -> Wait:
        return Wait.until(
            Timer.by_duration(timedelta(seconds=request.report_delay_seconds))
        )

    def execute(self, context: Context, request: CreditCheckRequest) -> StepDecision:
        return graceful_complete(f"credit report for {request.applicant_id}")


class CreditCheckFlow(Flow[CreditCheckRequest]):
    """The child Flow that LoanApplicationFlow starts."""

    def __init__(self) -> None:
        self.await_credit_report = AwaitCreditReport()

    def get_steps(self) -> StepList[CreditCheckRequest]:
        return StepList.start_step(self.await_credit_report)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
