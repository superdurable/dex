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

"""Runs the Decision with Deadline Steps beside a gated Step.

The Flow stays open after the decision, so late responses reach the window check.
"""

from __future__ import annotations

import asyncio

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    dead_end,
    go_to,
    go_to_many,
    rpc,
)

from dex_examples.patterns.deferred_choice.offer_response_flow import (
    OFFER_ACCEPTED,
    OFFER_REJECTED,
    OFFER_RESPONSE_WINDOW,
    AwaitOfferResponse,
    CloseRejectedOffer,
    ExpireOffer,
    FulfillOffer,
    RouteResponseBeforeExpiry,
    publish_while_window_open,
)
from tests.integ.condition_order_probe import PROBE_GATE_OPENED


HELD_RESPONSE_TIMEOUT_SECONDS = 60


class HeldResponse:
    """Lets a test hold a response RPC inside its window lock."""

    def __init__(self) -> None:
        self.entered = asyncio.Event()
        self.released = asyncio.Event()


HELD_RESPONSE = HeldResponse()


class OpenOfferProbe(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return go_to_many(
            StepMovement.of(AwaitOfferResponse, offer_id),
            StepMovement.of(HoldOfferProbeOpen, None),
        )


class HoldOfferProbeOpen(Step[None]):
    """Keeps the Flow open until the test opens the gate."""

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(PROBE_GATE_OPENED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return dead_end()


class OfferResponseWindowProbeFlow(Flow[str]):
    def __init__(self) -> None:
        self.open_offer_probe = OpenOfferProbe()
        self.hold_offer_probe_open = HoldOfferProbeOpen()
        self.await_offer_response = AwaitOfferResponse()
        self.fulfill_offer = FulfillOffer()
        self.close_rejected_offer = CloseRejectedOffer()
        self.route_response_before_expiry = RouteResponseBeforeExpiry()
        self.expire_offer = ExpireOffer()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.open_offer_probe).other_steps(
            self.hold_offer_probe_open,
            self.await_offer_response,
            self.fulfill_offer,
            self.close_rejected_offer,
            self.route_response_before_expiry,
            self.expire_offer,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            PROBE_GATE_OPENED, OFFER_ACCEPTED, OFFER_REJECTED, OFFER_RESPONSE_WINDOW
        )

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    def accept_probe_offer(self, context: Context, note: str) -> RPCResult[str]:
        return publish_while_window_open(context, OFFER_ACCEPTED, note)

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    def reject_probe_offer(self, context: Context, note: str) -> RPCResult[str]:
        return publish_while_window_open(context, OFFER_REJECTED, note)

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    async def accept_probe_offer_when_released(
        self, context: Context, note: str
    ) -> RPCResult[str]:
        """Holds the window lock until the test releases it."""
        HELD_RESPONSE.entered.set()
        await asyncio.wait_for(HELD_RESPONSE.released.wait(), HELD_RESPONSE_TIMEOUT_SECONDS)
        return publish_while_window_open(context, OFFER_ACCEPTED, note)

    @rpc
    def open_probe_gate(self, context: Context) -> None:
        PROBE_GATE_OPENED.publish(context, None)


OFFER_DECISION_GATE_OPENED = Channel("offerDecisionGateOpened", type(None))


class HoldOfferDecision(Step[str]):
    """Starts AwaitOfferResponse only after the test opens the decision gate."""

    def wait_for(self, context: Context, offer_id: str) -> Wait:
        return Wait.until(OFFER_DECISION_GATE_OPENED.for_one())

    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return go_to(AwaitOfferResponse, offer_id)


class OfferResponseDoubleReplyProbeFlow(Flow[str]):
    """Holds AwaitOfferResponse behind a gate, so both replies commit before any Execute."""

    def __init__(self) -> None:
        self.hold_offer_decision = HoldOfferDecision()
        self.await_offer_response = AwaitOfferResponse()
        self.fulfill_offer = FulfillOffer()
        self.close_rejected_offer = CloseRejectedOffer()
        self.route_response_before_expiry = RouteResponseBeforeExpiry()
        self.expire_offer = ExpireOffer()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.hold_offer_decision).other_steps(
            self.await_offer_response,
            self.fulfill_offer,
            self.close_rejected_offer,
            self.route_response_before_expiry,
            self.expire_offer,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            OFFER_DECISION_GATE_OPENED, OFFER_ACCEPTED, OFFER_REJECTED, OFFER_RESPONSE_WINDOW
        )

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    def accept_gated_offer(self, context: Context, note: str) -> RPCResult[str]:
        return publish_while_window_open(context, OFFER_ACCEPTED, note)

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    def reject_gated_offer(self, context: Context, note: str) -> RPCResult[str]:
        return publish_while_window_open(context, OFFER_REJECTED, note)

    @rpc
    def open_offer_decision_gate(self, context: Context) -> None:
        OFFER_DECISION_GATE_OPENED.publish(context, None)
