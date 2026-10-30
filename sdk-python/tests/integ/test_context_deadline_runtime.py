# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import asyncio
from datetime import datetime, timedelta, timezone

import pytest

from dex import (
    Context,
    Flow,
    FlowStatus,
    RetryPolicy,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    StartFlowOptions,
    Wait,
    graceful_complete,
    force_complete,
    rpc,
)

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .shared import unique_id


class DeadlineStep(Step[None]):
    def __init__(self, total_duration: timedelta | None) -> None:
        self.total_duration = total_duration
        self.observations: list[tuple[str, int, float]] = []

    def get_step_options(self) -> StepOptions:
        retry = RetryPolicy(
            initial_interval=timedelta(seconds=1),
            maximum_attempts=2,
            total_duration=self.total_duration,
        )
        return StepOptions(
            wait_for_method_timeout=timedelta(seconds=10),
            execute_method_timeout=timedelta(seconds=10),
            wait_for_retry=retry,
            execute_retry=retry,
        )

    def wait_for(self, context: Context, input: None) -> Wait:
        self._observe_deadline("wait_for", context)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        self._observe_deadline("execute", context)
        return graceful_complete()

    def _observe_deadline(self, method: str, context: Context) -> None:
        started_at = datetime.now(timezone.utc)
        assert context.deadline is not None
        assert context.deadline.tzinfo is timezone.utc
        remaining_seconds = (context.deadline - started_at).total_seconds()
        self.observations.append((method, context.attempt, remaining_seconds))
        if context.attempt == 1:
            raise RuntimeError("retry to observe the next attempt deadline")


class DeadlineFlow(Flow[None]):
    def __init__(self, total_duration: timedelta | None) -> None:
        self.start = DeadlineStep(total_duration)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)


class DeadlineRPCFlow(Flow[None]):
    def handle_timeout(self, context: Context) -> StepDecision:
        assert context.deadline is not None
        assert context.deadline.tzinfo is timezone.utc
        assert context.deadline > datetime.now(timezone.utc)
        return force_complete()

    @rpc
    def get_deadline_presence(self, context: Context) -> RPCResult[bool]:
        return RPCResult(context.deadline is not None)


def _assert_deadlines(flow: DeadlineFlow, total_seconds: int | None) -> None:
    assert [(method, attempt) for method, attempt, _ in flow.start.observations] == [
        ("wait_for", 1),
        ("wait_for", 2),
        ("execute", 1),
        ("execute", 2),
    ]
    for _, attempt, remaining_seconds in flow.start.observations:
        expected_seconds = (
            10 if total_seconds is None else total_seconds - (attempt - 1)
        )
        assert expected_seconds - 2 <= remaining_seconds <= expected_seconds + 0.5


@pytest.mark.parametrize("total_seconds", [None, 6])
@pytest.mark.parametrize("is_async", [False, True])
def test_context_deadlines(total_seconds: int | None, is_async: bool) -> None:
    flow = DeadlineFlow(
        None if total_seconds is None else timedelta(seconds=total_seconds)
    )
    rpc_flow = DeadlineRPCFlow()
    if is_async:
        asyncio.run(_test_async_deadlines(flow, rpc_flow))
    else:
        with DexDevTestEnvironment(flow, rpc_flow) as environment:
            flow_id = unique_id("deadline")
            environment.client.start_flow(flow, flow_id, None)
            assert (
                environment.client.wait_for_flow(flow_id, timedelta(seconds=30)).status
                is FlowStatus.COMPLETED
            )
            rpc_id = unique_id("rpc-deadline")
            environment.client.start_flow(
                rpc_flow,
                rpc_id,
                None,
                StartFlowOptions(timeout=timedelta(seconds=1)),
            )
            assert (
                environment.client.invoke_rpc(rpc_flow.get_deadline_presence, rpc_id)
                is False
            )
            assert (
                environment.client.wait_for_flow(rpc_id, timedelta(seconds=30)).status
                is FlowStatus.COMPLETED
            )
    _assert_deadlines(flow, total_seconds)


async def _test_async_deadlines(flow: DeadlineFlow, rpc_flow: DeadlineRPCFlow) -> None:
    async with AsyncDexDevTestEnvironment(flow, rpc_flow) as environment:
        flow_id = unique_id("async-deadline")
        await environment.client.start_flow(flow, flow_id, None)
        result = await environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert result.status is FlowStatus.COMPLETED
        rpc_id = unique_id("async-rpc-deadline")
        await environment.client.start_flow(
            rpc_flow,
            rpc_id,
            None,
            StartFlowOptions(timeout=timedelta(seconds=1)),
        )
        assert (
            await environment.client.invoke_rpc(rpc_flow.get_deadline_presence, rpc_id)
            is False
        )
        result = await environment.client.wait_for_flow(rpc_id, timedelta(seconds=30))
        assert result.status is FlowStatus.COMPLETED
