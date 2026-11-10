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
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

from dex_examples.patterns.deferred_choice.offer_response_flow import (
    OFFER_ACCEPTED,
    OFFER_EXPIRY,
    OFFER_REJECTED,
)
from tests.integ.condition_order_probe import PROBE_GATE_OPENED, ConditionOrderOutcome


class HoldOfferProbe(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(PROBE_GATE_OPENED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(AwaitOfferProbeResponse, None)


class AwaitOfferProbeResponse(Step[None]):
    """Declares its conditions in the same order as AwaitOfferResponse."""

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.any_of(
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
            Timer.by_duration(OFFER_EXPIRY),
        )

    def execute(self, context: Context, input: None) -> StepDecision:
        if OFFER_ACCEPTED.results(context):
            return graceful_complete(
                ConditionOrderOutcome("accepted", OFFER_REJECTED.size(context))
            )
        return graceful_complete(
            ConditionOrderOutcome("rejected", OFFER_ACCEPTED.size(context))
        )


class DeferredChoiceOrderProbeFlow(Flow[None]):
    """Waits like AwaitOfferResponse, after a gate."""

    def __init__(self) -> None:
        self.hold_offer_probe = HoldOfferProbe()
        self.await_offer_probe_response = AwaitOfferProbeResponse()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.hold_offer_probe).other_steps(
            self.await_offer_probe_response
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(PROBE_GATE_OPENED, OFFER_ACCEPTED, OFFER_REJECTED)

    @rpc
    def accept_probe_offer(self, context: Context, note: str) -> None:
        OFFER_ACCEPTED.publish(context, note)

    @rpc
    def reject_probe_offer(self, context: Context, note: str) -> None:
        OFFER_REJECTED.publish(context, note)

    @rpc
    def open_probe_gate(self, context: Context) -> None:
        PROBE_GATE_OPENED.publish(context, None)
