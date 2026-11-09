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

from dataclasses import dataclass

from dex import (
    Channel,
    ConditionCombination,
    Context,
    Flow,
    PersistenceSchema,
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


def require_branch_letter(value: str) -> str:
    if value not in BRANCH_LETTERS:
        raise ValueError(f"branch must be one of {', '.join(BRANCH_LETTERS)}, got {value}")
    return value


# WCP-9 Structured Discriminator and WCP-30 Structured Partial Join. Channel
# messages persist, so a reset Step consumes the late quotes.

SUPPLIER_A_RELEASED = Channel("supplierAReleased", str)
SUPPLIER_B_RELEASED = Channel("supplierBReleased", str)
SUPPLIER_C_RELEASED = Channel("supplierCReleased", str)
SUPPLIER_RELEASED = {
    "A": SUPPLIER_A_RELEASED,
    "B": SUPPLIER_B_RELEASED,
    "C": SUPPLIER_C_RELEASED,
}
QUOTE_RECEIVED = Channel("quoteReceived", str)
SUPPLIER_COUNT = len(BRANCH_LETTERS)


class RequestQuotes(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            StepMovement.of(QuoteFromSupplierA, None),
            StepMovement.of(QuoteFromSupplierB, None),
            StepMovement.of(QuoteFromSupplierC, None),
            StepMovement.of(AcceptQuotes, input),
        )


class QuoteFromSupplierA(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SUPPLIER_A_RELEASED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        QUOTE_RECEIVED.publish(context, "A")
        return dead_end()


class QuoteFromSupplierB(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SUPPLIER_B_RELEASED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        QUOTE_RECEIVED.publish(context, "B")
        return dead_end()


class QuoteFromSupplierC(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(SUPPLIER_C_RELEASED.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        QUOTE_RECEIVED.publish(context, "C")
        return dead_end()


class AcceptQuotes(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(QUOTE_RECEIVED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            StepMovement.of(PlaceOrder, sorted(QUOTE_RECEIVED.results(context))),
            StepMovement.of(ResetQuoteJoin, SUPPLIER_COUNT - input),
        )


class PlaceOrder(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        return graceful_complete(input)


class ResetQuoteJoin(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(QUOTE_RECEIVED.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(sorted(QUOTE_RECEIVED.results(context)))


class StructuredPartialJoinFlow(Flow[int]):
    def __init__(self) -> None:
        self.request_quotes = RequestQuotes()
        self.quote_from_supplier_a = QuoteFromSupplierA()
        self.quote_from_supplier_b = QuoteFromSupplierB()
        self.quote_from_supplier_c = QuoteFromSupplierC()
        self.accept_quotes = AcceptQuotes()
        self.place_order = PlaceOrder()
        self.reset_quote_join = ResetQuoteJoin()

    def get_steps(self) -> StepList[int]:
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


# WCP-28 Blocking Discriminator and WCP-31 Blocking Partial Join. Per-branch
# Channels hold next-round signals until the reset Step re-arms the join.


@dataclass(frozen=True)
class BlockingJoinRound:
    required_signals: int
    round: int


@dataclass(frozen=True)
class ProcessedRound:
    round: int
    branches: list[str]


@dataclass(frozen=True)
class PendingJoinReset:
    required_signals: int
    round: int
    pending_branches: list[str]


BRANCH_A_SIGNAL = Channel("branchASignal", str)
BRANCH_B_SIGNAL = Channel("branchBSignal", str)
BRANCH_C_SIGNAL = Channel("branchCSignal", str)
BRANCH_SIGNALS = {"A": BRANCH_A_SIGNAL, "B": BRANCH_B_SIGNAL, "C": BRANCH_C_SIGNAL}
BLOCKING_JOIN_ROUNDS = 2


class BlockingJoin(Step[BlockingJoinRound]):
    def wait_for(self, context: Context, input: BlockingJoinRound) -> Wait:
        if input.required_signals == 1:
            return Wait.any_of(
                BRANCH_A_SIGNAL.for_one(),
                BRANCH_B_SIGNAL.for_one(),
                BRANCH_C_SIGNAL.for_one(),
            )
        return Wait.any_combination_of(
            ConditionCombination.of(
                BRANCH_A_SIGNAL.for_one(condition_id="ab-a"),
                BRANCH_B_SIGNAL.for_one(condition_id="ab-b"),
            ),
            ConditionCombination.of(
                BRANCH_A_SIGNAL.for_one(condition_id="ac-a"),
                BRANCH_C_SIGNAL.for_one(condition_id="ac-c"),
            ),
            ConditionCombination.of(
                BRANCH_B_SIGNAL.for_one(condition_id="bc-b"),
                BRANCH_C_SIGNAL.for_one(condition_id="bc-c"),
            ),
        )

    def execute(self, context: Context, input: BlockingJoinRound) -> StepDecision:
        fired = [
            letter for letter in BRANCH_LETTERS if BRANCH_SIGNALS[letter].results(context)
        ]
        pending = [letter for letter in BRANCH_LETTERS if letter not in fired]
        return go_to_many(
            StepMovement.of(ProcessRound, ProcessedRound(input.round, fired)),
            StepMovement.of(
                ResetBlockingJoin,
                PendingJoinReset(input.required_signals, input.round, pending),
            ),
        )


class ProcessRound(Step[ProcessedRound]):
    def execute(self, context: Context, input: ProcessedRound) -> StepDecision:
        return graceful_complete(input)


class ResetBlockingJoin(Step[PendingJoinReset]):
    def wait_for(self, context: Context, input: PendingJoinReset) -> Wait:
        return Wait.all_of(
            *(
                BRANCH_SIGNALS[require_branch_letter(branch)].for_one()
                for branch in input.pending_branches
            )
        )

    def execute(self, context: Context, input: PendingJoinReset) -> StepDecision:
        if input.round < BLOCKING_JOIN_ROUNDS:
            return go_to(
                BlockingJoin, BlockingJoinRound(input.required_signals, input.round + 1)
            )
        return graceful_complete(input.round)


class BlockingPartialJoinFlow(Flow[BlockingJoinRound]):
    def __init__(self) -> None:
        self.blocking_join = BlockingJoin()
        self.process_round = ProcessRound()
        self.reset_blocking_join = ResetBlockingJoin()

    def get_steps(self) -> StepList[BlockingJoinRound]:
        return StepList.start_step(self.blocking_join).other_steps(
            self.process_round, self.reset_blocking_join
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BRANCH_A_SIGNAL, BRANCH_B_SIGNAL, BRANCH_C_SIGNAL)

    @rpc
    def send_branch_signal(self, context: Context, input: str) -> None:
        letter = require_branch_letter(input)
        BRANCH_SIGNALS[letter].publish(context, letter)


# WCP-29 Cancelling Discriminator and WCP-32 Cancelling Partial Join: the join
# cancels the branches still running.

CARRIER_A_RELEASED = Channel("carrierAReleased", str)
CARRIER_B_RELEASED = Channel("carrierBReleased", str)
CARRIER_C_RELEASED = Channel("carrierCReleased", str)
CARRIER_RELEASED = {
    "A": CARRIER_A_RELEASED,
    "B": CARRIER_B_RELEASED,
    "C": CARRIER_C_RELEASED,
}
BID_RECEIVED = Channel("bidReceived", str)


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
        return graceful_complete(
            sorted(BID_RECEIVED.results(context))
        ).with_canceling_steps(BidFromCarrierA, BidFromCarrierB, BidFromCarrierC)


class CancellingPartialJoinFlow(Flow[int]):
    def __init__(self) -> None:
        self.request_bids = RequestBids()
        self.bid_from_carrier_a = BidFromCarrierA()
        self.bid_from_carrier_b = BidFromCarrierB()
        self.bid_from_carrier_c = BidFromCarrierC()
        self.accept_bids = AcceptBids()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.request_bids).other_steps(
            self.bid_from_carrier_a,
            self.bid_from_carrier_b,
            self.bid_from_carrier_c,
            self.accept_bids,
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            CARRIER_A_RELEASED, CARRIER_B_RELEASED, CARRIER_C_RELEASED, BID_RECEIVED
        )

    @rpc
    def release_carrier_bid(self, context: Context, input: str) -> None:
        CARRIER_RELEASED[require_branch_letter(input)].publish(context, "released")
