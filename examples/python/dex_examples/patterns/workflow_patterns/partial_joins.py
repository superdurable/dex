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

from dex import (
    Channel,
    ChannelMap,
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
    graceful_complete,
    rpc,
)

BRANCH_LETTERS = ("A", "B", "C")
BRANCH_COUNT = len(BRANCH_LETTERS)


def require_branch_letter(value: str) -> str:
    if value not in BRANCH_LETTERS:
        raise ValueError(f"branch must be one of {', '.join(BRANCH_LETTERS)}, got {value}")
    return value


# Signals are keyed by round, so a later signal never counts toward an earlier round.
def round_instance(round: int) -> str:
    return f"round-{round}"


# WCP-9 (required_quotes 1) and WCP-30 (required_quotes 2).

# A reset Step consumes the late quotes of a round, then re-enables the join.


@dataclass(frozen=True)
class QuoteRound:
    required_quotes: int
    round: int
    round_count: int


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
QUOTE_RECEIVED = ChannelMap("quoteReceived", str)


class RequestQuotes(Step[QuoteRound]):
    def execute(self, context: Context, input: QuoteRound) -> StepDecision:
        return go_to_many(
            StepMovement.of(QuoteFromSupplierA, input.round),
            StepMovement.of(QuoteFromSupplierB, input.round),
            StepMovement.of(QuoteFromSupplierC, input.round),
            StepMovement.of(AcceptQuotes, input),
        )


class QuoteFromSupplierA(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(SUPPLIER_A_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        QUOTE_RECEIVED.publish(context, round_instance(input), "A")
        return dead_end()


class QuoteFromSupplierB(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(SUPPLIER_B_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        QUOTE_RECEIVED.publish(context, round_instance(input), "B")
        return dead_end()


class QuoteFromSupplierC(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(SUPPLIER_C_RELEASED.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        QUOTE_RECEIVED.publish(context, round_instance(input), "C")
        return dead_end()


class AcceptQuotes(Step[QuoteRound]):
    def wait_for(self, context: Context, input: QuoteRound) -> Wait:
        return Wait.until(
            QUOTE_RECEIVED.for_n(round_instance(input.round), input.required_quotes)
        )

    def execute(self, context: Context, input: QuoteRound) -> StepDecision:
        accepted = sorted(QUOTE_RECEIVED.results(context, round_instance(input.round)))
        return go_to_many(
            StepMovement.of(PlaceOrder, AcceptedQuotes(input.round, accepted)),
            StepMovement.of(ResetQuoteJoin, input),
        )


class PlaceOrder(Step[AcceptedQuotes]):
    def execute(self, context: Context, input: AcceptedQuotes) -> StepDecision:
        return graceful_complete(input)


class ResetQuoteJoin(Step[QuoteRound]):
    def wait_for(self, context: Context, input: QuoteRound) -> Wait:
        remaining_quotes = BRANCH_COUNT - input.required_quotes
        return Wait.until(QUOTE_RECEIVED.for_n(round_instance(input.round), remaining_quotes))

    def execute(self, context: Context, input: QuoteRound) -> StepDecision:
        if input.round < input.round_count:
            return go_to(RequestQuotes, replace(input, round=input.round + 1))
        ignored = sorted(QUOTE_RECEIVED.results(context, round_instance(input.round)))
        return graceful_complete(IgnoredQuotes(input.round, ignored))


class StructuredPartialJoinFlow(Flow[QuoteRound]):
    def __init__(self) -> None:
        self.request_quotes = RequestQuotes()
        self.quote_from_supplier_a = QuoteFromSupplierA()
        self.quote_from_supplier_b = QuoteFromSupplierB()
        self.quote_from_supplier_c = QuoteFromSupplierC()
        self.accept_quotes = AcceptQuotes()
        self.place_order = PlaceOrder()
        self.reset_quote_join = ResetQuoteJoin()

    def get_steps(self) -> StepList[QuoteRound]:
        return StepList.start_step(self.request_quotes).other_steps(
            self.quote_from_supplier_a,
            self.quote_from_supplier_b,
            self.quote_from_supplier_c,
            self.accept_quotes,
            self.place_order,
            self.reset_quote_join,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            SUPPLIER_A_RELEASED, SUPPLIER_B_RELEASED, SUPPLIER_C_RELEASED, QUOTE_RECEIVED
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


@dataclass(frozen=True)
class ProcessedRound:
    round: int
    branches: list[str]


BRANCH_A_SIGNAL = Channel("branchASignal", str)
BRANCH_B_SIGNAL = Channel("branchBSignal", str)
BRANCH_C_SIGNAL = Channel("branchCSignal", str)
BRANCH_SIGNALS = {"A": BRANCH_A_SIGNAL, "B": BRANCH_B_SIGNAL, "C": BRANCH_C_SIGNAL}
BRANCH_COMPLETED = ChannelMap("branchCompleted", str)
BLOCKING_JOIN_ROUNDS = 2


class StartBlockingRound(Step[BlockingJoinRound]):
    def execute(self, context: Context, input: BlockingJoinRound) -> StepDecision:
        return go_to_many(
            StepMovement.of(SignalFromBranchA, input.round),
            StepMovement.of(SignalFromBranchB, input.round),
            StepMovement.of(SignalFromBranchC, input.round),
            StepMovement.of(BlockingJoin, input),
        )


class SignalFromBranchA(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(BRANCH_A_SIGNAL.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        BRANCH_COMPLETED.publish(context, round_instance(input), "A")
        return dead_end()


class SignalFromBranchB(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(BRANCH_B_SIGNAL.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        BRANCH_COMPLETED.publish(context, round_instance(input), "B")
        return dead_end()


class SignalFromBranchC(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(BRANCH_C_SIGNAL.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        BRANCH_COMPLETED.publish(context, round_instance(input), "C")
        return dead_end()


class BlockingJoin(Step[BlockingJoinRound]):
    def wait_for(self, context: Context, input: BlockingJoinRound) -> Wait:
        return Wait.until(
            BRANCH_COMPLETED.for_n(round_instance(input.round), input.required_signals)
        )

    def execute(self, context: Context, input: BlockingJoinRound) -> StepDecision:
        fired = sorted(BRANCH_COMPLETED.results(context, round_instance(input.round)))
        return go_to_many(
            StepMovement.of(ProcessRound, ProcessedRound(input.round, fired)),
            StepMovement.of(ResetBlockingJoin, input),
        )


class ProcessRound(Step[ProcessedRound]):
    def execute(self, context: Context, input: ProcessedRound) -> StepDecision:
        return graceful_complete(input)


class ResetBlockingJoin(Step[BlockingJoinRound]):
    def wait_for(self, context: Context, input: BlockingJoinRound) -> Wait:
        remaining_signals = BRANCH_COUNT - input.required_signals
        return Wait.until(
            BRANCH_COMPLETED.for_n(round_instance(input.round), remaining_signals)
        )

    def execute(self, context: Context, input: BlockingJoinRound) -> StepDecision:
        if input.round < BLOCKING_JOIN_ROUNDS:
            return go_to(StartBlockingRound, replace(input, round=input.round + 1))
        return graceful_complete(input.round)


class BlockingPartialJoinFlow(Flow[BlockingJoinRound]):
    def __init__(self) -> None:
        self.start_blocking_round = StartBlockingRound()
        self.signal_from_branch_a = SignalFromBranchA()
        self.signal_from_branch_b = SignalFromBranchB()
        self.signal_from_branch_c = SignalFromBranchC()
        self.blocking_join = BlockingJoin()
        self.process_round = ProcessRound()
        self.reset_blocking_join = ResetBlockingJoin()

    def get_steps(self) -> StepList[BlockingJoinRound]:
        return StepList.start_step(self.start_blocking_round).other_steps(
            self.signal_from_branch_a,
            self.signal_from_branch_b,
            self.signal_from_branch_c,
            self.blocking_join,
            self.process_round,
            self.reset_blocking_join,
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
BID_RECEIVED = Channel("bidReceived", str)
CARRIER_BOOKING_CONFIRMED = Channel("carrierBookingConfirmed", str)


class RequestBids(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            StepMovement.of(BidFromCarrierA, None),
            StepMovement.of(BidFromCarrierB, None),
            StepMovement.of(BidFromCarrierC, None),
            StepMovement.of(AcceptBids, input),
        )


class BidFromCarrierA(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CARRIER_A_RELEASED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        BID_RECEIVED.publish(context, "A")
        return dead_end()


class BidFromCarrierB(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CARRIER_B_RELEASED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        BID_RECEIVED.publish(context, "B")
        return dead_end()


class BidFromCarrierC(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(CARRIER_C_RELEASED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        BID_RECEIVED.publish(context, "C")
        return dead_end()


class AcceptBids(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(BID_RECEIVED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to(
            ConfirmCarrierBooking, sorted(BID_RECEIVED.results(context))
        ).with_canceling_steps(BidFromCarrierA, BidFromCarrierB, BidFromCarrierC)


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
