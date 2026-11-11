# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import timedelta
from typing import Any, Callable, TypeVar

from dex import (
    AsyncContext,
    Channel,
    ChannelMap,
    Context,
    Flow,
    Join,
    JoinRound,
    PersistenceSchema,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Timer,
    Wait,
    WaitForFailurePolicy,
    dead_end,
    force_complete,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

StartT = TypeVar("StartT")

BRANCH_RESULT = Join("branchResult", str)
BRANCH_RELEASE = ChannelMap("branchRelease", str)
JOIN_RELEASE = Channel("joinRelease", str)
REQUIRED_QUOTES = 2
LAST_QUOTE_ROUND = 2


@dataclass
class JoinOutcome:
    branch_count: int
    results: list[str]


@dataclass
class ReviewInput:
    reviewer: str
    join_round: JoinRound


@dataclass
class QuoteRequest:
    round_number: int = 1
    previous_round: JoinRound | None = None
    accepted_rounds: list[list[str]] = field(default_factory=list)
    late_rounds: list[list[str]] = field(default_factory=list)


@dataclass
class QuoteJoinInput:
    request: QuoteRequest
    join_round: JoinRound


@dataclass
class JoinDeadlineOutcome:
    has_timer_fired: bool
    results: list[str]


class ReleasedBranch(Step[JoinRound]):
    label = ""

    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(BRANCH_RELEASE.for_one(self.label))

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return BRANCH_RESULT.end_branch(context, input, self.label)


class JoinBranchA(ReleasedBranch):
    label = "A"


class JoinBranchB(ReleasedBranch):
    label = "B"


class JoinBranchC(ReleasedBranch):
    label = "C"


def build_branch(
    step: type[ReleasedBranch],
) -> Callable[[JoinRound], StepMovement[Any]]:
    return lambda join_round: StepMovement.of(step, join_round)


def build_collect_all(join_round: JoinRound) -> StepMovement[Any]:
    return StepMovement.of(CollectAllBranches, join_round)


class CollectAllBranches(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(BRANCH_RESULT.for_all_branches(input))

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        results = BRANCH_RESULT.branch_results(context, input)
        return graceful_complete(JoinOutcome(input.branch_count, list(results)))


class ReleasableJoinFlow(Flow[StartT]):
    @rpc
    def release_branch(self, context: Context, label: str) -> None:
        BRANCH_RELEASE.publish(context, label, label)

    @rpc
    def release_join(self, context: Context) -> None:
        JOIN_RELEASE.publish(context, "release")

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BRANCH_RESULT, BRANCH_RELEASE, JOIN_RELEASE)


class SplitWithHeldJoin(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context,
            [
                build_branch(JoinBranchA),
                build_branch(JoinBranchB),
                build_branch(JoinBranchC),
            ],
            lambda join_round: StepMovement.of(HoldJoin, join_round),
        )
        return go_to_many(*movements)


class HoldJoin(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(JOIN_RELEASE.for_one())

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return go_to(CollectAllBranches, input)


class AllBranchesJoinFlow(ReleasableJoinFlow[None]):
    def get_flow_type(self) -> str:
        return "PythonAllBranchesJoinFlow"

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(SplitWithHeldJoin()).other_steps(
            JoinBranchA(),
            JoinBranchB(),
            JoinBranchC(),
            HoldJoin(),
            CollectAllBranches(),
        )


def build_review(reviewer: str) -> Callable[[JoinRound], StepMovement[Any]]:
    return lambda join_round: StepMovement.of(
        ReviewBranch, ReviewInput(reviewer, join_round)
    )


class SplitReviews(Step[list[str]]):
    def execute(self, context: Context, input: list[str]) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context,
            [build_review(reviewer) for reviewer in input],
            build_collect_all,
        )
        return go_to_many(*movements)


class ReviewBranch(Step[ReviewInput]):
    def execute(self, context: Context, input: ReviewInput) -> StepDecision:
        return BRANCH_RESULT.end_branch(context, input.join_round, input.reviewer)


class MultiInstanceJoinFlow(Flow[list[str]]):
    def get_flow_type(self) -> str:
        return "PythonMultiInstanceJoinFlow"

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(SplitReviews()).other_steps(
            ReviewBranch(), CollectAllBranches()
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BRANCH_RESULT)


def build_async_review(reviewer: str) -> Callable[[JoinRound], StepMovement[Any]]:
    return lambda join_round: StepMovement.of(
        AsyncReviewBranch, ReviewInput(reviewer, join_round)
    )


class AsyncSplitReviews(Step[list[str]]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: list[str]
    ) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context,
            [build_async_review(reviewer) for reviewer in input],
            build_async_collect_all,
        )
        return go_to_many(*movements)


class AsyncReviewBranch(Step[ReviewInput]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: ReviewInput
    ) -> StepDecision:
        return BRANCH_RESULT.end_branch(context, input.join_round, input.reviewer)


class AsyncCollectAllBranches(Step[JoinRound]):
    async def wait_for(  # type: ignore[override]
        self, context: AsyncContext, input: JoinRound
    ) -> Wait:
        return Wait.until(BRANCH_RESULT.for_all_branches(input))

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: JoinRound
    ) -> StepDecision:
        results = BRANCH_RESULT.branch_results(context, input)
        return graceful_complete(JoinOutcome(input.branch_count, list(results)))


def build_async_collect_all(join_round: JoinRound) -> StepMovement[Any]:
    return StepMovement.of(AsyncCollectAllBranches, join_round)


class AsyncMultiInstanceJoinFlow(Flow[list[str]]):
    def get_flow_type(self) -> str:
        return "PythonAsyncMultiInstanceJoinFlow"

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(AsyncSplitReviews()).other_steps(
            AsyncReviewBranch(), AsyncCollectAllBranches()
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BRANCH_RESULT)


class RequestQuotes(Step[QuoteRequest]):
    def wait_for(self, context: Context, input: QuoteRequest) -> Wait:
        if input.previous_round is None:
            return Wait.skip_immediately()
        return Wait.until(
            BRANCH_RESULT.for_remaining_branches(input.previous_round, REQUIRED_QUOTES)
        )

    def execute(self, context: Context, input: QuoteRequest) -> StepDecision:
        if input.previous_round is not None:
            late_results = BRANCH_RESULT.remaining_branch_results(
                context, input.previous_round
            )
            input.late_rounds.append(list(late_results))
        movements = BRANCH_RESULT.fork(
            context,
            [
                build_branch(JoinBranchA),
                build_branch(JoinBranchB),
                build_branch(JoinBranchC),
            ],
            lambda join_round: StepMovement.of(
                AcceptQuotes, QuoteJoinInput(input, join_round)
            ),
        )
        return go_to_many(*movements)


class AcceptQuotes(Step[QuoteJoinInput]):
    def wait_for(self, context: Context, input: QuoteJoinInput) -> Wait:
        return Wait.until(BRANCH_RESULT.for_branches(input.join_round, REQUIRED_QUOTES))

    def execute(self, context: Context, input: QuoteJoinInput) -> StepDecision:
        accepted = BRANCH_RESULT.branch_results(context, input.join_round)
        request = input.request
        request.accepted_rounds.append(list(accepted))
        if request.round_number == LAST_QUOTE_ROUND:
            return graceful_complete(request)
        next_request = QuoteRequest(
            request.round_number + 1,
            input.join_round,
            request.accepted_rounds,
            request.late_rounds,
        )
        return go_to(RequestQuotes, next_request)


class PartialJoinRoundsFlow(ReleasableJoinFlow[QuoteRequest]):
    def get_flow_type(self) -> str:
        return "PythonPartialJoinRoundsFlow"

    def get_steps(self) -> StepList[QuoteRequest]:
        return StepList.start_step(RequestQuotes()).other_steps(
            JoinBranchA(), JoinBranchB(), JoinBranchC(), AcceptQuotes()
        )


class SplitOneBranch(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context, [build_branch(JoinBranchA)], build_collect_all
        )
        return go_to_many(*movements)


class KeyIsolationJoinFlow(ReleasableJoinFlow[None]):
    def get_flow_type(self) -> str:
        return "PythonKeyIsolationJoinFlow"

    @rpc
    def report_for_other_round(self, context: Context, join_round: JoinRound) -> None:
        BRANCH_RESULT.report_branch_result(context, join_round, "other-round")

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(SplitOneBranch()).other_steps(
            JoinBranchA(), CollectAllBranches()
        )


class SplitWithFirstBranchJoin(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context,
            [
                build_branch(JoinBranchA),
                build_branch(JoinBranchB),
                build_branch(JoinBranchC),
            ],
            lambda join_round: StepMovement.of(AcceptFirstBranch, join_round),
        )
        return go_to_many(*movements)


class AcceptFirstBranch(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.all_of(
            BRANCH_RESULT.for_first_branch(input), JOIN_RELEASE.for_one()
        )

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        winner = BRANCH_RESULT.branch_results(context, input)[0]
        return go_to(AwaitFinish, winner).with_canceling_sibling_steps(
            JoinBranchA, JoinBranchB, JoinBranchC
        )


class AwaitFinish(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        return Wait.until(JOIN_RELEASE.for_one())

    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)


class CancellingFirstBranchJoinFlow(ReleasableJoinFlow[None]):
    def get_flow_type(self) -> str:
        return "PythonCancellingFirstBranchJoinFlow"

    @rpc(load_channel_maps=(BRANCH_RESULT.channel_map,))
    def get_pending_results(
        self, context: Context, round_key: str
    ) -> RPCResult[list[str]]:
        pending = BRANCH_RESULT.channel_map.pending_messages(context, round_key)
        return RPCResult([message.value for message in pending])

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(SplitWithFirstBranchJoin()).other_steps(
            JoinBranchA(),
            JoinBranchB(),
            JoinBranchC(),
            AcceptFirstBranch(),
            AwaitFinish(),
        )


class SplitTwoBranches(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context,
            [build_branch(JoinBranchA), build_branch(JoinBranchB)],
            build_collect_all,
        )
        return go_to_many(*movements)


class JoinCanceledFallback(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(JOIN_RELEASE.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("join canceled")


class CancelJoinWithBranchesFlow(ReleasableJoinFlow[None]):
    def get_flow_type(self) -> str:
        return "PythonCancelJoinWithBranchesFlow"

    @rpc
    def cancel_join_with_branches(self, context: Context) -> RPCResult[None]:
        return RPCResult(
            None, next_steps=(StepMovement.of(JoinCanceledFallback, None),)
        ).with_canceling_steps(CollectAllBranches, JoinBranchA, JoinBranchB)

    @rpc(load_channel_maps=(BRANCH_RESULT.channel_map,))
    def get_pending_results(
        self, context: Context, round_key: str
    ) -> RPCResult[list[str]]:
        pending = BRANCH_RESULT.channel_map.pending_messages(context, round_key)
        return RPCResult([message.value for message in pending])

    @rpc
    def get_pending_release_count(self, context: Context, label: str) -> RPCResult[int]:
        return RPCResult(BRANCH_RELEASE.size(context, label))

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(SplitTwoBranches()).other_steps(
            JoinBranchA(), JoinBranchB(), CollectAllBranches(), JoinCanceledFallback()
        )


class ForkTwice(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        BRANCH_RESULT.fork(context, [build_branch(JoinBranchA)], build_collect_all)
        movements = BRANCH_RESULT.fork(
            context, [build_branch(JoinBranchA)], build_collect_all
        )
        return go_to_many(*movements)

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1)
        ).on_execute_failure_proceed_to(RecordForkFailure)


class RecordForkFailure(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        recovery_error = context.recovery_error
        detail = "" if recovery_error is None else recovery_error.detail
        return graceful_complete(detail)


class ForkTwiceJoinFlow(Flow[None]):
    def get_flow_type(self) -> str:
        return "PythonForkTwiceJoinFlow"

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(ForkTwice()).other_steps(
            RecordForkFailure(), JoinBranchA(), CollectAllBranches()
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BRANCH_RESULT, BRANCH_RELEASE)


class IdleStart(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        return dead_end()


class HandoffBranch(Step[JoinRound]):
    """Ends without reporting; an RPC reports this branch's result."""

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return dead_end()


class RpcForkJoinFlow(ReleasableJoinFlow[None]):
    def get_flow_type(self) -> str:
        return "PythonRpcForkJoinFlow"

    @rpc
    def fork_in_rpc(self, context: Context) -> RPCResult[None]:
        movements = BRANCH_RESULT.fork(
            context, [build_branch(JoinBranchA)], build_collect_all
        )
        return RPCResult(None, next_steps=movements)

    @rpc
    def fork_with_key_in_rpc(self, context: Context, round_key: str) -> RPCResult[None]:
        movements = BRANCH_RESULT.fork_with_round_key(
            round_key,
            [
                build_branch(JoinBranchA),
                lambda join_round: StepMovement.of(HandoffBranch, join_round),
            ],
            build_collect_all,
        )
        return RPCResult(None, next_steps=movements)

    @rpc
    def report_handoff_result(self, context: Context, join_round: JoinRound) -> None:
        BRANCH_RESULT.report_branch_result(context, join_round, "Y-from-rpc")

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(IdleStart()).other_steps(
            JoinBranchA(), HandoffBranch(), CollectAllBranches()
        )


MANUAL_ROUND = JoinRound("manual-round", 2)


class ValidateJoinCondition(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        if input == "first-of-zero-branches":
            return Wait.until(
                BRANCH_RESULT.for_first_branch(JoinRound("empty-round", 0))
            )
        if input == "branches-zero":
            return Wait.until(BRANCH_RESULT.for_branches(MANUAL_ROUND, 0))
        if input == "branches-over":
            return Wait.until(BRANCH_RESULT.for_branches(MANUAL_ROUND, 3))
        if input == "remaining-negative":
            return Wait.until(BRANCH_RESULT.for_remaining_branches(MANUAL_ROUND, -1))
        if input == "remaining-over":
            return Wait.until(BRANCH_RESULT.for_remaining_branches(MANUAL_ROUND, 3))
        return Wait.until(BRANCH_RESULT.for_remaining_branches(MANUAL_ROUND, 2))

    def execute(self, context: Context, input: str) -> StepDecision:
        if context.wait_for_method_failed():
            return graceful_complete(["wait_for failed"])
        return graceful_complete(
            list(BRANCH_RESULT.remaining_branch_results(context, MANUAL_ROUND))
        )

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=1),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
        )


class JoinConditionValidationFlow(Flow[str]):
    def get_flow_type(self) -> str:
        return "PythonJoinConditionValidationFlow"

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(ValidateJoinCondition())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(BRANCH_RESULT)


class DuplicateJoinNameFlow(Flow[None]):
    def get_flow_type(self) -> str:
        return "PythonDuplicateJoinNameFlow"

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(IdleStart())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            Join("duplicateName", str), ChannelMap("duplicateName", str)
        )


class SplitWithDeadlineJoin(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        movements = BRANCH_RESULT.fork(
            context,
            [build_branch(JoinBranchA), build_branch(JoinBranchB)],
            lambda join_round: StepMovement.of(CollectWithDeadline, join_round),
        )
        return go_to_many(*movements)


class CollectWithDeadline(Step[JoinRound]):
    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.any_of(
            BRANCH_RESULT.for_all_branches(input, condition_id="all-branches"),
            Timer.by_duration(timedelta(seconds=3), condition_id="deadline"),
        )

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        results = BRANCH_RESULT.branch_results(context, input)
        return force_complete(
            JoinDeadlineOutcome(context.has_timer_fired(), list(results))
        )


class JoinDeadlineFlow(ReleasableJoinFlow[None]):
    def get_flow_type(self) -> str:
        return "PythonJoinDeadlineFlow"

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(SplitWithDeadlineJoin()).other_steps(
            JoinBranchA(), JoinBranchB(), CollectWithDeadline()
        )
