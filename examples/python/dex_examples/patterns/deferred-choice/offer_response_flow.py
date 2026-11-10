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

from datetime import timedelta

from dex import (
    Channel,
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

# How long the customer has to respond.
OFFER_EXPIRY = timedelta(days=3)

OFFER_ACCEPTED = Channel("offerAccepted", str)
OFFER_REJECTED = Channel("offerRejected", str)


class AwaitOfferResponse(Step[str]):
    """Waits for whichever event happens first and routes on it."""

    def wait_for(self, context: Context, offer_id: str) -> Wait:
        return Wait.any_of(
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
            Timer.by_duration(OFFER_EXPIRY),
        )

    def execute(self, context: Context, offer_id: str) -> StepDecision:
        if context.has_timer_fired():
            return go_to(ExpireOffer, offer_id)
        if OFFER_ACCEPTED.results(context):
            return go_to(FulfillOffer, offer_id)
        return go_to(CloseRejectedOffer, offer_id)


class FulfillOffer(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return graceful_complete(f"fulfilled {offer_id}")


class CloseRejectedOffer(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return graceful_complete(f"rejected {offer_id}")


class ExpireOffer(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return graceful_complete(f"expired {offer_id}")


class OfferResponseFlow(Flow[str]):
    """Routes on the first of three events: accept, reject, or expiry."""

    def __init__(self) -> None:
        self.await_offer_response = AwaitOfferResponse()
        self.fulfill_offer = FulfillOffer()
        self.close_rejected_offer = CloseRejectedOffer()
        self.expire_offer = ExpireOffer()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.await_offer_response).other_steps(
            self.fulfill_offer, self.close_rejected_offer, self.expire_offer
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(OFFER_ACCEPTED, OFFER_REJECTED)

    @rpc
    def accept_customer_offer(self, context: Context, note: str) -> None:
        """Records that the customer accepted the offer."""
        OFFER_ACCEPTED.publish(context, note)

    @rpc
    def reject_customer_offer(self, context: Context, note: str) -> None:
        """Records that the customer rejected the offer."""
        OFFER_REJECTED.publish(context, note)
