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

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.cancel_case.loan_application_flow import (
    APPLICATION_CANCELED,
    UNDERWRITER_DECIDED,
)
from tests.integ.condition_order_probe import PROBE_GATE_OPENED, ConditionOrderOutcome


class HoldDecisionProbe(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(PROBE_GATE_OPENED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(AwaitProbeDecision, None)


class AwaitProbeDecision(Step[None]):
    """Declares its conditions in the same order as AwaitUnderwriterDecision."""

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.any_of(APPLICATION_CANCELED.for_one(), UNDERWRITER_DECIDED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        if APPLICATION_CANCELED.results(context):
            return graceful_complete(
                ConditionOrderOutcome("canceled", UNDERWRITER_DECIDED.size(context))
            )
        return graceful_complete(
            ConditionOrderOutcome("decided", APPLICATION_CANCELED.size(context))
        )


class CancelCaseOrderProbeFlow(Flow[None]):
    """Waits like AwaitUnderwriterDecision, after a gate."""

    def __init__(self) -> None:
        self.hold_decision_probe = HoldDecisionProbe()
        self.await_probe_decision = AwaitProbeDecision()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.hold_decision_probe).other_steps(
            self.await_probe_decision
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            PROBE_GATE_OPENED, APPLICATION_CANCELED, UNDERWRITER_DECIDED
        )

    @rpc
    def cancel_probe_application(self, context: Context, reason: str) -> None:
        APPLICATION_CANCELED.publish(context, reason)

    @rpc
    def decide_probe_application(self, context: Context, decision: str) -> None:
        UNDERWRITER_DECIDED.publish(context, decision)

    @rpc
    def open_probe_gate(self, context: Context) -> None:
        PROBE_GATE_OPENED.publish(context, None)
