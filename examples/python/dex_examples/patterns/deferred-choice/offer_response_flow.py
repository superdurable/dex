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
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Timer,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)

# How long the customer has to respond.
OFFER_EXPIRY = timedelta(days=3)

# The response window is open until AwaitOfferResponse decides. Unset reads as open.
RESPONSE_WINDOW_OPEN = "open"
RESPONSE_WINDOW_CLOSED = "closed"

# The outcomes of a customer response RPC.
OFFER_RESPONSE_RECORDED = "recorded"
OFFER_RESPONSE_WINDOW_CLOSED = "window closed"

OFFER_ACCEPTED = Channel("offerAccepted", str)
OFFER_REJECTED = Channel("offerRejected", str)
# Locked by both response RPCs and by AwaitOfferResponse.execute.
OFFER_RESPONSE_WINDOW = Attribute("offerResponseWindow", str)


def read_response_window(context: Context) -> str:
    """Returns RESPONSE_WINDOW_OPEN while the window was never closed."""
    return OFFER_RESPONSE_WINDOW.get(context) or RESPONSE_WINDOW_OPEN


def publish_while_window_open(
    context: Context, response: Channel[str], note: str
) -> RPCResult[str]:
    """Publishes the response unless the window is closed.

    The publish commits together with this check, because the RPC holds the window lock.
    """
    if read_response_window(context) == RESPONSE_WINDOW_CLOSED:
        return RPCResult(OFFER_RESPONSE_WINDOW_CLOSED)
    response.publish(context, note)
    return RPCResult(OFFER_RESPONSE_RECORDED)


# dex:group group-id:decide group-label:"Decide"
# dex:explanation text:"Waits for an acceptance, a rejection, or the 3-day deadline, closes the response window, and routes on the winner."
class AwaitOfferResponse(Step[str]):
    def get_step_options(self) -> StepOptions:
        """Locks the response window in execute, the same lock as the response RPCs."""
        return StepOptions(execute_lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))

    def wait_for(self, context: Context, offer_id: str) -> Wait:
        return Wait.any_of(
            OFFER_ACCEPTED.for_one(),
            OFFER_REJECTED.for_one(),
            Timer.by_duration(OFFER_EXPIRY),
        )

    def execute(self, context: Context, offer_id: str) -> StepDecision:
        # Every later response RPC now returns OFFER_RESPONSE_WINDOW_CLOSED.
        OFFER_RESPONSE_WINDOW.set(context, RESPONSE_WINDOW_CLOSED)
        if context.has_timer_fired():
            # A response committed after the Timer won but before this execute took the lock.
            if OFFER_ACCEPTED.size(context) + OFFER_REJECTED.size(context) > 0:
                return go_to(RouteResponseBeforeExpiry, offer_id)
            return go_to(ExpireOffer, offer_id)
        if OFFER_ACCEPTED.results(context):
            return go_to(FulfillOffer, offer_id)
        return go_to(CloseRejectedOffer, offer_id)


# dex:group group-id:decide group-label:"Decide"
# dex:explanation text:"Takes the response that the customer sent before the window closed and routes on it."
class RouteResponseBeforeExpiry(Step[str]):
    def wait_for(self, context: Context, offer_id: str) -> Wait:
        return Wait.any_of(OFFER_ACCEPTED.for_one(), OFFER_REJECTED.for_one())

    def execute(self, context: Context, offer_id: str) -> StepDecision:
        if OFFER_ACCEPTED.results(context):
            return go_to(FulfillOffer, offer_id)
        return go_to(CloseRejectedOffer, offer_id)


# dex:group group-id:outcome group-label:"Outcome"
# dex:explanation text:"Completes the Flow with the fulfilled offer."
class FulfillOffer(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return graceful_complete(f"fulfilled {offer_id}")


# dex:group group-id:outcome group-label:"Outcome"
# dex:explanation text:"Completes the Flow with the rejected offer."
class CloseRejectedOffer(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return graceful_complete(f"rejected {offer_id}")


# dex:group group-id:outcome group-label:"Outcome"
# dex:explanation text:"Completes the Flow with the expired offer."
class ExpireOffer(Step[str]):
    def execute(self, context: Context, offer_id: str) -> StepDecision:
        return graceful_complete(f"expired {offer_id}")


class OfferResponseFlow(Flow[str]):
    """Routes on the first of three events: accept, reject, or expiry."""

    def __init__(self) -> None:
        self.await_offer_response = AwaitOfferResponse()
        self.fulfill_offer = FulfillOffer()
        self.close_rejected_offer = CloseRejectedOffer()
        self.route_response_before_expiry = RouteResponseBeforeExpiry()
        self.expire_offer = ExpireOffer()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.await_offer_response).other_steps(
            self.fulfill_offer,
            self.close_rejected_offer,
            self.route_response_before_expiry,
            self.expire_offer,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(OFFER_ACCEPTED, OFFER_REJECTED, OFFER_RESPONSE_WINDOW)

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    def accept_customer_offer(self, context: Context, note: str) -> RPCResult[str]:
        """Records that the customer accepted the offer.

        Returns OFFER_RESPONSE_WINDOW_CLOSED once the offer has decided.
        """
        return publish_while_window_open(context, OFFER_ACCEPTED, note)

    @rpc(lock_attributes=(OFFER_RESPONSE_WINDOW.lock(),))
    def reject_customer_offer(self, context: Context, note: str) -> RPCResult[str]:
        """Records that the customer rejected the offer.

        Returns OFFER_RESPONSE_WINDOW_CLOSED once the offer has decided.
        """
        return publish_while_window_open(context, OFFER_REJECTED, note)
