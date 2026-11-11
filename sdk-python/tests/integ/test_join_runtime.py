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

import asyncio
from datetime import timedelta

import pytest

from dex import (
    AsyncClient,
    FlowDefinitionError,
    JoinRound,
    Registry,
    RequestTimeoutError,
    StepExecutionId,
    WaitForStepCompletionOptions,
    WorkerInvocationError,
)

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .join_flow import (
    AllBranchesJoinFlow,
    AsyncMultiInstanceJoinFlow,
    CancellingFirstBranchJoinFlow,
    DuplicateJoinNameFlow,
    ForkTwiceJoinFlow,
    JoinConditionValidationFlow,
    JoinDeadlineFlow,
    JoinDeadlineOutcome,
    JoinOutcome,
    KeyIsolationJoinFlow,
    MultiInstanceJoinFlow,
    PartialJoinRoundsFlow,
    QuoteRequest,
    ReportForCanceledBranchJoinFlow,
    RpcForkJoinFlow,
)
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)
NOT_STARTED_TIMEOUT = timedelta(seconds=2)


def test_all_branches_join_reads_results_reported_before_it_waits() -> None:
    asyncio.run(_all_branches_join_reads_results_reported_before_it_waits())


async def _all_branches_join_reads_results_reported_before_it_waits() -> None:
    flow = AllBranchesJoinFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-all-branches")
        await client.start_flow(flow, flow_id, None)
        for label in ("C", "A", "B"):
            await client.invoke_rpc(flow.release_branch, flow_id, label)
            await _wait_for_step(client, flow_id, f"JoinBranch{label}")
        await client.invoke_rpc(flow.release_join, flow_id)
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(JoinOutcome) == JoinOutcome(3, ["C", "A", "B"])


@pytest.mark.parametrize(
    "reviewers",
    [["r1", "r2", "r3", "r4"], []],
    ids=["four-instances", "zero-instances"],
)
def test_multi_instance_join_counts_branches_from_the_input_list(
    reviewers: list[str],
) -> None:
    flow = MultiInstanceJoinFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-join-multi-instance")
        environment.client.start_flow(flow, flow_id, reviewers)
        outcome = environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT).single_output(
            JoinOutcome
        )
        assert outcome.branch_count == len(reviewers)
        assert sorted(outcome.results) == reviewers


def test_async_handlers_fork_and_end_branches() -> None:
    asyncio.run(_async_handlers_fork_and_end_branches())


async def _async_handlers_fork_and_end_branches() -> None:
    flow = AsyncMultiInstanceJoinFlow()
    async with AsyncDexDevTestEnvironment(
        flow, allow_async_handlers=True
    ) as environment:
        flow_id = unique_id("python-join-async-handlers")
        await environment.client.start_flow(flow, flow_id, ["r1", "r2", "r3"])
        result = await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        outcome = result.single_output(JoinOutcome)
        assert outcome.branch_count == 3
        assert sorted(outcome.results) == ["r1", "r2", "r3"]


def test_partial_join_rounds_reset_and_block_the_next_round() -> None:
    asyncio.run(_partial_join_rounds_reset_and_block_the_next_round())


async def _partial_join_rounds_reset_and_block_the_next_round() -> None:
    flow = PartialJoinRoundsFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-partial-rounds")
        await client.start_flow(flow, flow_id, QuoteRequest())
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        await _wait_for_step(client, flow_id, "JoinBranchA")
        await client.invoke_rpc(flow.release_branch, flow_id, "B")
        await _wait_for_step(client, flow_id, "AcceptQuotes")

        # Round 2 enablement delivered early must wait for the round 1 reset.
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        await _assert_step_not_completed(client, flow_id, "RequestQuotes", 2)

        await client.invoke_rpc(flow.release_branch, flow_id, "C")
        await _wait_for_step(client, flow_id, "RequestQuotes", 2)
        await _wait_for_step(client, flow_id, "JoinBranchA", 2)
        await client.invoke_rpc(flow.release_branch, flow_id, "B")
        await _wait_for_step(client, flow_id, "AcceptQuotes", 2)
        await client.invoke_rpc(flow.release_branch, flow_id, "C")

        request = (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
            QuoteRequest
        )
        assert request.accepted_rounds == [["A", "B"], ["A", "B"]]
        assert request.late_rounds == [["C"]]


def test_results_for_another_round_key_do_not_satisfy_the_join() -> None:
    asyncio.run(_results_for_another_round_key_do_not_satisfy_the_join())


async def _results_for_another_round_key_do_not_satisfy_the_join() -> None:
    flow = KeyIsolationJoinFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-key-isolation")
        await client.start_flow(flow, flow_id, None)
        await client.invoke_rpc(
            flow.report_for_other_round, flow_id, JoinRound("other-round", 1)
        )
        await _assert_step_not_completed(client, flow_id, "CollectAllBranches")
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(JoinOutcome) == JoinOutcome(1, ["A"])


def test_first_branch_join_cancels_siblings_and_keeps_late_results_pending() -> None:
    asyncio.run(_first_branch_join_cancels_siblings_and_keeps_late_results_pending())


async def _first_branch_join_cancels_siblings_and_keeps_late_results_pending() -> None:
    flow = CancellingFirstBranchJoinFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-cancel-siblings")
        await client.start_flow(flow, flow_id, None)
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        await _wait_for_step(client, flow_id, "JoinBranchA")
        await client.invoke_rpc(flow.release_branch, flow_id, "B")
        await _wait_for_step(client, flow_id, "JoinBranchB")
        await client.invoke_rpc(flow.release_join, flow_id)
        await _wait_for_step(client, flow_id, "AcceptFirstBranch")

        await client.invoke_rpc(flow.release_branch, flow_id, "C")
        await _wait_for_step(client, flow_id, "JoinBranchC")
        pending_results = await client.invoke_rpc(
            flow.get_pending_results, flow_id, "SplitWithFirstBranchJoin-1"
        )
        assert pending_results == ["B"]

        await client.invoke_rpc(flow.release_join, flow_id)
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(str) == "A"


def test_rpc_reports_for_a_canceled_branch_so_the_join_fires() -> None:
    asyncio.run(_rpc_reports_for_a_canceled_branch_so_the_join_fires())


async def _rpc_reports_for_a_canceled_branch_so_the_join_fires() -> None:
    flow = ReportForCanceledBranchJoinFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-report-canceled")
        await client.start_flow(flow, flow_id, None)
        await _wait_for_step(client, flow_id, "SplitTwoBranches")
        await client.invoke_rpc(
            flow.cancel_branch_b, flow_id, JoinRound("SplitTwoBranches-1", 2)
        )
        await _wait_for_step(client, flow_id, "JoinBranchB")
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(JoinOutcome) == JoinOutcome(2, ["B-canceled", "A"])


def test_second_fork_of_one_join_in_an_invocation_fails_the_step() -> None:
    asyncio.run(_second_fork_of_one_join_in_an_invocation_fails_the_step())


async def _second_fork_of_one_join_in_an_invocation_fails_the_step() -> None:
    flow = ForkTwiceJoinFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-join-fork-twice")
        await environment.client.start_flow(flow, flow_id, None)
        result = await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        detail = result.single_output(str)
        assert "Join branchResult was already forked in this Step execution" in detail


def test_rpc_forks_only_with_an_explicit_round_key() -> None:
    asyncio.run(_rpc_forks_only_with_an_explicit_round_key())


async def _rpc_forks_only_with_an_explicit_round_key() -> None:
    flow = RpcForkJoinFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-rpc-fork")
        await client.start_flow(flow, flow_id, None)
        with pytest.raises(WorkerInvocationError) as fork_failure:
            await client.invoke_rpc(flow.fork_in_rpc, flow_id)
        assert (
            "requires a Step execute Context" in fork_failure.value.worker_error_detail
        )
        with pytest.raises(WorkerInvocationError) as key_failure:
            await client.invoke_rpc(flow.fork_with_key_in_rpc, flow_id, "round/1")
        assert "must not contain '/'" in key_failure.value.worker_error_detail

        await client.invoke_rpc(flow.fork_with_key_in_rpc, flow_id, "rpc-round-1")
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(JoinOutcome) == JoinOutcome(1, ["A"])


@pytest.mark.parametrize(
    "validation_case",
    [
        "branches-zero",
        "branches-over",
        "remaining-negative",
        "remaining-over",
        "first-of-zero-branches",
    ],
)
def test_invalid_join_counts_fail_wait_for(validation_case: str) -> None:
    assert _run_join_condition_validation(validation_case) == ["wait_for failed"]


def test_reset_with_nothing_remaining_completes_immediately() -> None:
    assert _run_join_condition_validation("remaining-none") == []


def _run_join_condition_validation(validation_case: str) -> list[str]:
    return asyncio.run(_join_condition_validation(validation_case))


async def _join_condition_validation(validation_case: str) -> list[str]:
    flow = JoinConditionValidationFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id(f"python-join-validation-{validation_case}")
        await environment.client.start_flow(flow, flow_id, validation_case)
        result = await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        return result.single_output(list[str])


def test_join_and_channel_map_with_one_name_fail_registration() -> None:
    with pytest.raises(FlowDefinitionError, match="duplicate persistence definition"):
        Registry([DuplicateJoinNameFlow()])


def test_join_deadline_timer_wins_when_a_branch_never_reports() -> None:
    asyncio.run(_join_deadline_timer_wins_when_a_branch_never_reports())


async def _join_deadline_timer_wins_when_a_branch_never_reports() -> None:
    flow = JoinDeadlineFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client
        flow_id = unique_id("python-join-deadline")
        await client.start_flow(flow, flow_id, None)
        await client.invoke_rpc(flow.release_branch, flow_id, "A")
        result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        assert result.single_output(JoinDeadlineOutcome) == JoinDeadlineOutcome(
            True, []
        )


async def _wait_for_step(
    client: AsyncClient, flow_id: str, step_type: str, number: int = 1
) -> None:
    await client.wait_for_step_completion(
        flow_id,
        StepExecutionId(step_type, number),
        WaitForStepCompletionOptions(f"{flow_id}-{step_type}-{number}", WAIT_TIMEOUT),
    )


async def _assert_step_not_completed(
    client: AsyncClient, flow_id: str, step_type: str, number: int = 1
) -> None:
    with pytest.raises(RequestTimeoutError):
        await client.wait_for_step_completion(
            flow_id,
            StepExecutionId(step_type, number),
            WaitForStepCompletionOptions(
                f"{flow_id}-{step_type}-{number}-not-started", NOT_STARTED_TIMEOUT
            ),
        )
