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
from typing import Callable

from dex import (
    AsyncClient,
    AsyncContext,
    Attribute,
    Channel,
    Context,
    Flow,
    FlowClosingError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    FlowStatus,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StopFlowOptions,
    StopType,
    SubFlow,
    SubFlowOptions,
    SubFlowReusePolicy,
    Wait,
    force_fail,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.cancel_case.credit_check_flow import (
    CreditCheckFlow,
    CreditCheckRequest,
)

# The child's own Flow timeout. It stops the child even when the parent
# closes without stopping it.
CREDIT_CHECK_TIMEOUT = timedelta(days=7)

APPLICATION_CANCELED_PREFIX = "application canceled: "


@dataclass(frozen=True)
class LoanApplication:
    """The input of LoanApplicationFlow."""

    applicant_id: str = field(metadata={"dex_json_name": "applicantId"})
    credit_report_delay_seconds: int = field(
        metadata={"dex_json_name": "creditReportDelaySeconds"}
    )


STOPPED_CREDIT_CHECK_FLOW_ID = Attribute("stoppedCreditCheckFlowId", str)
APPLICATION_CANCELED = Channel("applicationCanceled", str)
UNDERWRITER_DECIDED = Channel("underwriterDecided", str)


class RunCreditCheck(Step[LoanApplication]):
    """Waits for its child Flow or a cancellation, whichever comes first."""

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        credit_check_flow: CreditCheckFlow,
    ) -> None:
        self.client_provider = client_provider
        self.credit_check_flow = credit_check_flow

    def wait_for(self, context: Context, application: LoanApplication) -> Wait:
        request = CreditCheckRequest(
            applicant_id=application.applicant_id,
            report_delay_seconds=application.credit_report_delay_seconds,
        )
        # The cancel condition comes first: it wins when both are ready.
        return Wait.any_of(
            APPLICATION_CANCELED.for_one(),
            SubFlow.run(
                self.credit_check_flow,
                request,
                SubFlowOptions(
                    timeout=CREDIT_CHECK_TIMEOUT,
                    reuse_policy=SubFlowReusePolicy.RESTART_IF_PREVIOUS_EXITS_ABNORMALLY,
                ),
            ),
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, application: LoanApplication
    ) -> StepDecision:
        reasons = APPLICATION_CANCELED.results(context)
        if reasons:
            child_flow_id = SubFlow.get_flow_id(context)
            await self.stop_credit_check(child_flow_id)
            STOPPED_CREDIT_CHECK_FLOW_ID.set(context, child_flow_id)
            return force_fail(APPLICATION_CANCELED_PREFIX + reasons[0])
        result = SubFlow.get_condition_results(context)
        if result.status is not FlowStatus.COMPLETED:
            return force_fail(f"credit check ended with status {result.status.value}")
        return go_to(AwaitUnderwriterDecision, result.single_output(str))

    async def stop_credit_check(self, child_flow_id: str) -> None:
        """Cancels the child; a missing or already closed child counts as stopped."""
        try:
            await self.client_provider().stop_flow(
                child_flow_id,
                StopFlowOptions(type=StopType.CANCEL, reason="loan application canceled"),
            )
        except (FlowNotFoundError, FlowNotActiveOrNotFoundError, FlowClosingError):
            return


class AwaitUnderwriterDecision(Step[str]):
    """Also waits for the cancel Channel, so a late cancellation still closes the application."""

    def wait_for(self, context: Context, report: str) -> Wait:
        return Wait.any_of(APPLICATION_CANCELED.for_one(), UNDERWRITER_DECIDED.for_one())

    def execute(self, context: Context, report: str) -> StepDecision:
        reasons = APPLICATION_CANCELED.results(context)
        if reasons:
            return force_fail(APPLICATION_CANCELED_PREFIX + reasons[0])
        decisions = UNDERWRITER_DECIDED.results(context)
        return graceful_complete(f"{decisions[0]} after {report}")


class LoanApplicationFlow(Flow[LoanApplication]):
    """Runs a credit check in a child Flow.

    Canceling the application also stops the child Flow.
    """

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        credit_check_flow: CreditCheckFlow,
    ) -> None:
        self.run_credit_check = RunCreditCheck(client_provider, credit_check_flow)
        self.await_underwriter_decision = AwaitUnderwriterDecision()

    def get_steps(self) -> StepList[LoanApplication]:
        return StepList.start_step(self.run_credit_check).other_steps(
            self.await_underwriter_decision
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            STOPPED_CREDIT_CHECK_FLOW_ID, APPLICATION_CANCELED, UNDERWRITER_DECIDED
        )

    @rpc
    def cancel_application(self, context: Context, reason: str) -> None:
        """Cancels the application and the credit check it started."""
        APPLICATION_CANCELED.publish(context, reason)

    @rpc
    def decide_application(self, context: Context, decision: str) -> None:
        """Records the underwriter's decision."""
        UNDERWRITER_DECIDED.publish(context, decision)
