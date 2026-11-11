# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from dataclasses import dataclass

from dex import (
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
    dead_end,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

QUOTE_RECEIVED = Join("quoteReceived", str)
SUPPLIER_RELEASED = Channel("supplierReleased", str)
REQUIRED_QUOTES = 2


@dataclass
class QuoteRequest:
    round_number: int
    previous_round: JoinRound | None = None


@dataclass
class QuoteJoinInput:
    request: QuoteRequest
    join_round: JoinRound


@dataclass
class ReviewInput:
    reviewer: str
    join_round: JoinRound


def build_review(reviewer):
    return lambda join_round: StepMovement.of(ReviewDocument, ReviewInput(reviewer, join_round))


def build_collect_reviews(join_round):
    return StepMovement.of(CollectReviews, join_round)


class RequestQuotes(Step[QuoteRequest]):
    def wait_for(self, context: Context, input: QuoteRequest) -> Wait:
        if input.previous_round is None:
            return Wait.skip_immediately()
        return Wait.until(QUOTE_RECEIVED.for_remaining_branches(input.previous_round, REQUIRED_QUOTES))

    def execute(self, context: Context, input: QuoteRequest) -> StepDecision:
        movements = QUOTE_RECEIVED.fork(
            context,
            [
                lambda join_round: StepMovement.of(QuoteFromSupplierA, join_round),
                lambda join_round: StepMovement.of(QuoteFromSupplierB, join_round),
            ],
            lambda join_round: StepMovement.of(AcceptQuotes, QuoteJoinInput(input, join_round)),
        )
        return go_to_many(*movements)


class QuoteFromSupplierA(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(SUPPLIER_RELEASED.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return QUOTE_RECEIVED.end_branch(context, input, "A")


class QuoteFromSupplierB(Step[JoinRound]):
    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        QUOTE_RECEIVED.report_branch_result(context, input, "B")
        return dead_end()


class AcceptQuotes(Step[QuoteJoinInput]):
    def wait_for(self, context: Context, input: QuoteJoinInput) -> Wait:
        return Wait.until(QUOTE_RECEIVED.for_branches(input.join_round, REQUIRED_QUOTES))

    def execute(self, context: Context, input: QuoteJoinInput) -> StepDecision:
        accepted = QUOTE_RECEIVED.branch_results(context, input.join_round)
        if input.request.round_number == 2:
            return graceful_complete(list(accepted))
        return go_to(RequestQuotes, QuoteRequest(input.request.round_number + 1, input.join_round))


class ReviewDocument(Step[ReviewInput]):
    def execute(self, context: Context, input: ReviewInput) -> StepDecision:
        return QUOTE_RECEIVED.end_branch(context, input.join_round, input.reviewer)


class CollectReviews(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(QUOTE_RECEIVED.for_all_branches(input))

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return graceful_complete(list(QUOTE_RECEIVED.branch_results(context, input)))


class QuoteFlow(Flow[QuoteRequest]):
    @rpc
    def request_reviews(self, context: Context, reviewers: list[str]) -> RPCResult[None]:
        movements = QUOTE_RECEIVED.fork_with_round_key(
            "reviews",
            [build_review(reviewer) for reviewer in reviewers],
            build_collect_reviews,
        )
        return RPCResult(None, next_steps=movements)

    def get_steps(self) -> StepList[QuoteRequest]:
        return StepList.start_step(RequestQuotes()).other_steps(
            QuoteFromSupplierA(),
            QuoteFromSupplierB(),
            AcceptQuotes(),
            ReviewDocument(),
            CollectReviews(),
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(QUOTE_RECEIVED, SUPPLIER_RELEASED)
