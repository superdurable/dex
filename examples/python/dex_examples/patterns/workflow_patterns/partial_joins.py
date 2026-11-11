# Copyright (c) 2026 Super Durable, Inc.
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

"""Discriminators and partial joins: WCP-9, WCP-28 to WCP-32."""

from __future__ import annotations

from dataclasses import dataclass, replace
from typing import Any

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    Join,
    JoinRound,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

BRANCH_LETTERS = ("A", "B", "C")


def require_branch_letter(value: str) -> str:
    if value not in BRANCH_LETTERS:
        raise ValueError(f"branch must be one of {', '.join(BRANCH_LETTERS)}, got {value}")
    return value


# WCP-9 (required_quotes 1) and WCP-30 (required_quotes 2).

# The next RequestQuotes consumes the late quotes of a round, then re-enables the join.


@dataclass(frozen=True)
class QuoteRound:
    required_quotes: int
    round: int
    round_count: int
    previous_round: JoinRound | None = None


@dataclass(frozen=True)
class QuoteJoin:
    quote_round: QuoteRound
    join_round: JoinRound


@dataclass(frozen=True)
class AcceptedQuotes:
    round: int
    accepted_quotes: list[str]


@dataclass(frozen=True)
class IgnoredQuotes:
    round: int
    ignored_quotes: list[str]


SUPPLIER_A_RELEASED = Channel("supplierAReleased", str)
SUPPLIER_B_RELEASED = Channel("supplierBReleased", str)
SUPPLIER_C_RELEASED = Channel("supplierCReleased", str)
SUPPLIER_RELEASED = {
    "A": SUPPLIER_A_RELEASED,
    "B": SUPPLIER_B_RELEASED,
    "C": SUPPLIER_C_RELEASED,
}
QUOTE_RECEIVED = Join("quoteReceived", str)
# The late quotes of the most recently reset round.
IGNORED_QUOTES = Attribute("ignoredQuotes", IgnoredQuotes)


class RequestQuotes(Step[QuoteRound]):
    def wait_for(self, context: Context, input: QuoteRound) -> Wait:
        if input.previous_round is None:
            return Wait.skip_immediately()
        return Wait.until(
            QUOTE_RECEIVED.for_remaining_branches(input.previous_round, input.required_quotes)
        )

    def execute(self, context: Context, input: QuoteRound) -> StepDecision:
        if input.previous_round is not None:
            ignored = sorted(
                QUOTE_RECEIVED.remaining_branch_results(context, input.previous_round)
            )
            IGNORED_QUOTES.set(context, IgnoredQuotes(input.round - 1, ignored))
        return go_to_many(
            *QUOTE_RECEIVED.fork(
                context,
                [
                    lambda join_round: StepMovement.of(QuoteFromSupplierA, join_round),
                    lambda join_round: StepMovement.of(QuoteFromSupplierB, join_round),
                    lambda join_round: StepMovement.of(QuoteFromSupplierC, join_round),
                ],
                lambda join_round: StepMovement.of(AcceptQuotes, QuoteJoin(input, join_round)),
            )
        )


class QuoteFromSupplierA(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(SUPPLIER_A_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return QUOTE_RECEIVED.end_branch(context, input, "A")


class QuoteFromSupplierB(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(SUPPLIER_B_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return QUOTE_RECEIVED.end_branch(context, input, "B")


class QuoteFromSupplierC(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(SUPPLIER_C_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return QUOTE_RECEIVED.end_branch(context, input, "C")


class AcceptQuotes(Step[QuoteJoin]):
    def wait_for(self, context: Context, input: QuoteJoin) -> Wait:
        return Wait.until(
            QUOTE_RECEIVED.for_branches(input.join_round, input.quote_round.required_quotes)
        )

    def execute(self, context: Context, input: QuoteJoin) -> StepDecision:
        quote_round = input.quote_round
        accepted = sorted(QUOTE_RECEIVED.branch_results(context, input.join_round))
        movements: list[StepMovement[Any]] = [
            StepMovement.of(PlaceOrder, AcceptedQuotes(quote_round.round, accepted))
        ]
        # The last round's late quotes stay pending until the Flow closes.
        if quote_round.round < quote_round.round_count:
            next_round = replace(
                quote_round, round=quote_round.round + 1, previous_round=input.join_round
            )
            movements.append(StepMovement.of(RequestQuotes, next_round))
        return go_to_many(*movements)


class PlaceOrder(Step[AcceptedQuotes]):
    def execute(self, context: Context, input: AcceptedQuotes) -> StepDecision:
        return graceful_complete(input)


class StructuredPartialJoinFlow(Flow[QuoteRound]):
    def __init__(self) -> None:
        self.request_quotes = RequestQuotes()
        self.quote_from_supplier_a = QuoteFromSupplierA()
        self.quote_from_supplier_b = QuoteFromSupplierB()
        self.quote_from_supplier_c = QuoteFromSupplierC()
        self.accept_quotes = AcceptQuotes()
        self.place_order = PlaceOrder()

    def get_steps(self) -> StepList[QuoteRound]:
        return StepList.start_step(self.request_quotes).other_steps(
            self.quote_from_supplier_a,
            self.quote_from_supplier_b,
            self.quote_from_supplier_c,
            self.accept_quotes,
            self.place_order,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            SUPPLIER_A_RELEASED,
            SUPPLIER_B_RELEASED,
            SUPPLIER_C_RELEASED,
            QUOTE_RECEIVED,
            IGNORED_QUOTES,
        )

    @rpc
    def release_supplier_quote(self, context: Context, input: str) -> None:
        SUPPLIER_RELEASED[require_branch_letter(input)].publish(context, "released")


# WCP-28 (required_signals 1) and WCP-31 (required_signals 2).

# No Step waits on round k + 1 before round k resets, so a next-round signal stays queued.


@dataclass(frozen=True)
class BlockingJoinRound:
    required_signals: int
    round: int
    previous_round: JoinRound | None = None


@dataclass(frozen=True)
class BlockingJoinInput:
    blocking_round: BlockingJoinRound
    join_round: JoinRound


@dataclass(frozen=True)
class ProcessedRound:
    round: int
    branches: list[str]


BRANCH_A_SIGNAL = Channel("branchASignal", str)
BRANCH_B_SIGNAL = Channel("branchBSignal", str)
BRANCH_C_SIGNAL = Channel("branchCSignal", str)
BRANCH_SIGNALS = {"A": BRANCH_A_SIGNAL, "B": BRANCH_B_SIGNAL, "C": BRANCH_C_SIGNAL}
BRANCH_COMPLETED = Join("branchCompleted", str)
BLOCKING_JOIN_ROUNDS = 2


class StartBlockingRound(Step[BlockingJoinRound]):
    def wait_for(self, context: Context, input: BlockingJoinRound) -> Wait:
        if input.previous_round is None:
            return Wait.skip_immediately()
        return Wait.until(
            BRANCH_COMPLETED.for_remaining_branches(
                input.previous_round, input.required_signals
            )
        )

    def execute(self, context: Context, input: BlockingJoinRound) -> StepDecision:
        return go_to_many(
            *BRANCH_COMPLETED.fork(
                context,
                [
                    lambda join_round: StepMovement.of(SignalFromBranchA, join_round),
                    lambda join_round: StepMovement.of(SignalFromBranchB, join_round),
                    lambda join_round: StepMovement.of(SignalFromBranchC, join_round),
                ],
                lambda join_round: StepMovement.of(
                    BlockingJoin, BlockingJoinInput(input, join_round)
                ),
            )
        )


class SignalFromBranchA(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(BRANCH_A_SIGNAL.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BRANCH_COMPLETED.end_branch(context, input, "A")


class SignalFromBranchB(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(BRANCH_B_SIGNAL.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BRANCH_COMPLETED.end_branch(context, input, "B")


class SignalFromBranchC(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(BRANCH_C_SIGNAL.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BRANCH_COMPLETED.end_branch(context, input, "C")


class BlockingJoin(Step[BlockingJoinInput]):
    def wait_for(self, context: Context, input: BlockingJoinInput) -> Wait:
        return Wait.until(
            BRANCH_COMPLETED.for_branches(
                input.join_round, input.blocking_round.required_signals
            )
        )

    def execute(self, context: Context, input: BlockingJoinInput) -> StepDecision:
        blocking_round = input.blocking_round
        fired = sorted(BRANCH_COMPLETED.branch_results(context, input.join_round))
        movements: list[StepMovement[Any]] = [
            StepMovement.of(ProcessRound, ProcessedRound(blocking_round.round, fired))
        ]
        # The last round's late signals stay pending until the Flow closes.
        if blocking_round.round < BLOCKING_JOIN_ROUNDS:
            next_round = replace(
                blocking_round,
                round=blocking_round.round + 1,
                previous_round=input.join_round,
            )
            movements.append(StepMovement.of(StartBlockingRound, next_round))
        return go_to_many(*movements)


class ProcessRound(Step[ProcessedRound]):
    def execute(self, context: Context, input: ProcessedRound) -> StepDecision:
        return graceful_complete(input)


class BlockingPartialJoinFlow(Flow[BlockingJoinRound]):
    def __init__(self) -> None:
        self.start_blocking_round = StartBlockingRound()
        self.signal_from_branch_a = SignalFromBranchA()
        self.signal_from_branch_b = SignalFromBranchB()
        self.signal_from_branch_c = SignalFromBranchC()
        self.blocking_join = BlockingJoin()
        self.process_round = ProcessRound()

    def get_steps(self) -> StepList[BlockingJoinRound]:
        return StepList.start_step(self.start_blocking_round).other_steps(
            self.signal_from_branch_a,
            self.signal_from_branch_b,
            self.signal_from_branch_c,
            self.blocking_join,
            self.process_round,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            BRANCH_A_SIGNAL, BRANCH_B_SIGNAL, BRANCH_C_SIGNAL, BRANCH_COMPLETED
        )

    @rpc
    def send_branch_signal(self, context: Context, input: str) -> None:
        letter = require_branch_letter(input)
        BRANCH_SIGNALS[letter].publish(context, letter)

    @rpc(load_channels=(BRANCH_A_SIGNAL, BRANCH_B_SIGNAL, BRANCH_C_SIGNAL))
    def get_queued_branch_signal_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(sum(channel.size(context) for channel in BRANCH_SIGNALS.values()))


# WCP-29 (required_bids 1) and WCP-32 (required_bids 2): the join cancels the branches
# still waiting.

CARRIER_A_RELEASED = Channel("carrierAReleased", str)
CARRIER_B_RELEASED = Channel("carrierBReleased", str)
CARRIER_C_RELEASED = Channel("carrierCReleased", str)
CARRIER_RELEASED = {
    "A": CARRIER_A_RELEASED,
    "B": CARRIER_B_RELEASED,
    "C": CARRIER_C_RELEASED,
}
BID_RECEIVED = Join("bidReceived", str)
CARRIER_BOOKING_CONFIRMED = Channel("carrierBookingConfirmed", str)


@dataclass(frozen=True)
class CarrierBidJoin:
    required_bids: int
    join_round: JoinRound


class RequestBids(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            *BID_RECEIVED.fork(
                context,
                [
                    lambda join_round: StepMovement.of(BidFromCarrierA, join_round),
                    lambda join_round: StepMovement.of(BidFromCarrierB, join_round),
                    lambda join_round: StepMovement.of(BidFromCarrierC, join_round),
                ],
                lambda join_round: StepMovement.of(
                    AcceptBids, CarrierBidJoin(input, join_round)
                ),
            )
        )


class BidFromCarrierA(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(CARRIER_A_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BID_RECEIVED.end_branch(context, input, "A")


class BidFromCarrierB(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(CARRIER_B_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BID_RECEIVED.end_branch(context, input, "B")


class BidFromCarrierC(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(CARRIER_C_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BID_RECEIVED.end_branch(context, input, "C")


class AcceptBids(Step[CarrierBidJoin]):
    def wait_for(self, context: Context, input: CarrierBidJoin) -> Wait:
        return Wait.until(BID_RECEIVED.for_branches(input.join_round, input.required_bids))

    def execute(self, context: Context, input: CarrierBidJoin) -> StepDecision:
        return go_to(
            ConfirmCarrierBooking,
            sorted(BID_RECEIVED.branch_results(context, input.join_round)),
        ).with_canceling_sibling_steps(BidFromCarrierA, BidFromCarrierB, BidFromCarrierC)


class ConfirmCarrierBooking(Step[list[str]]):
    def wait_for(self, context: Context, input: list[str]) -> Wait:
        return Wait.until(CARRIER_BOOKING_CONFIRMED.for_one())

    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete(input)


class CancellingPartialJoinFlow(Flow[int]):
    def __init__(self) -> None:
        self.request_bids = RequestBids()
        self.bid_from_carrier_a = BidFromCarrierA()
        self.bid_from_carrier_b = BidFromCarrierB()
        self.bid_from_carrier_c = BidFromCarrierC()
        self.accept_bids = AcceptBids()
        self.confirm_carrier_booking_step = ConfirmCarrierBooking()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.request_bids).other_steps(
            self.bid_from_carrier_a,
            self.bid_from_carrier_b,
            self.bid_from_carrier_c,
            self.accept_bids,
            self.confirm_carrier_booking_step,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            CARRIER_A_RELEASED,
            CARRIER_B_RELEASED,
            CARRIER_C_RELEASED,
            BID_RECEIVED,
            CARRIER_BOOKING_CONFIRMED,
        )

    @rpc
    def release_carrier_bid(self, context: Context, input: str) -> None:
        CARRIER_RELEASED[require_branch_letter(input)].publish(context, "released")

    # A canceled branch no longer waits, so its late release stays queued.
    @rpc(load_channels=(CARRIER_A_RELEASED, CARRIER_B_RELEASED, CARRIER_C_RELEASED))
    def get_queued_carrier_release_count(self, context: Context) -> RPCResult[int]:
        return RPCResult(sum(channel.size(context) for channel in CARRIER_RELEASED.values()))

    @rpc
    def confirm_carrier_booking(self, context: Context) -> None:
        CARRIER_BOOKING_CONFIRMED.publish(context, "confirmed")
