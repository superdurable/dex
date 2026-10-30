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
    force_complete,
    go_to,
    graceful_complete,
    rpc,
)

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .shared import unique_id


class StartedAtFirstStep(Step[None]):
    def __init__(self) -> None:
        self.observations: list[tuple[str, int, datetime]] = []

    def get_step_options(self) -> StepOptions:
        retry = RetryPolicy(initial_interval=timedelta(seconds=1), maximum_attempts=2)
        return StepOptions(wait_for_retry=retry, execute_retry=retry)

    def wait_for(self, context: Context, input: None) -> Wait:
        self._observe_started_at("wait_for", context)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: None) -> StepDecision:
        self._observe_started_at("execute", context)
        return go_to(StartedAtSecondStep, None)

    def _observe_started_at(self, method: str, context: Context) -> None:
        self.observations.append((method, context.attempt, context.flow_started_at))
        if context.attempt == 1:
            raise RuntimeError("retry to compare the Flow start time")


class StartedAtSecondStep(Step[None]):
    def __init__(self) -> None:
        self.started_at: datetime | None = None

    def execute(self, context: Context, input: None) -> StepDecision:
        self.started_at = context.flow_started_at
        return graceful_complete()


class StartedAtFlow(Flow[None]):
    def __init__(self) -> None:
        self.first = StartedAtFirstStep()
        self.second = StartedAtSecondStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.first).other_steps(self.second)


class StartedAtTimeoutFlow(Flow[None]):
    def __init__(self) -> None:
        self.timeout_started_at: datetime | None = None
        self.rpc_started_at: datetime | None = None

    def handle_timeout(self, context: Context) -> StepDecision:
        self.timeout_started_at = context.flow_started_at
        return force_complete()

    @rpc
    def get_flow_started_timestamp(self, context: Context) -> RPCResult[int]:
        self.rpc_started_at = context.flow_started_at
        return RPCResult(int(context.flow_started_at.timestamp()))


def _assert_started_at(flow: StartedAtFlow, expected: datetime) -> None:
    assert [(method, attempt) for method, attempt, _ in flow.first.observations] == [
        ("wait_for", 1),
        ("wait_for", 2),
        ("execute", 1),
        ("execute", 2),
    ]
    observed = [started_at for _, _, started_at in flow.first.observations]
    assert flow.second.started_at is not None
    observed.append(flow.second.started_at)
    for started_at in observed:
        assert started_at == expected.replace(microsecond=0)
        assert started_at.tzinfo is timezone.utc


def _assert_timeout_started_at(flow: StartedAtTimeoutFlow, expected: datetime) -> None:
    for started_at in (flow.timeout_started_at, flow.rpc_started_at):
        assert started_at == expected.replace(microsecond=0)
        assert started_at is not None
        assert started_at.tzinfo is timezone.utc


@pytest.mark.parametrize("is_async", [False, True])
def test_context_flow_started_at(is_async: bool) -> None:
    flow = StartedAtFlow()
    timeout_flow = StartedAtTimeoutFlow()
    if is_async:
        asyncio.run(_test_async_started_at(flow, timeout_flow))
        return
    with DexDevTestEnvironment(flow, timeout_flow) as environment:
        flow_id = unique_id("flow-started-at")
        environment.client.start_flow(flow, flow_id, None)
        assert (
            environment.client.wait_for_flow(flow_id, timedelta(seconds=30)).status
            is FlowStatus.COMPLETED
        )
        _assert_started_at(flow, environment.client.describe_flow(flow_id).started_at)
        timeout_id = unique_id("timeout-started-at")
        environment.client.start_flow(
            timeout_flow,
            timeout_id,
            None,
            StartFlowOptions(timeout=timedelta(seconds=3)),
        )
        expected = environment.client.describe_flow(timeout_id).started_at
        assert environment.client.invoke_rpc(
            timeout_flow.get_flow_started_timestamp, timeout_id
        ) == int(expected.timestamp())
        assert (
            environment.client.wait_for_flow(timeout_id, timedelta(seconds=30)).status
            is FlowStatus.COMPLETED
        )
        _assert_timeout_started_at(timeout_flow, expected)


async def _test_async_started_at(
    flow: StartedAtFlow, timeout_flow: StartedAtTimeoutFlow
) -> None:
    async with AsyncDexDevTestEnvironment(flow, timeout_flow) as environment:
        flow_id = unique_id("async-flow-started-at")
        await environment.client.start_flow(flow, flow_id, None)
        result = await environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert result.status is FlowStatus.COMPLETED
        info = await environment.client.describe_flow(flow_id)
        _assert_started_at(flow, info.started_at)
        timeout_id = unique_id("async-timeout-started-at")
        await environment.client.start_flow(
            timeout_flow,
            timeout_id,
            None,
            StartFlowOptions(timeout=timedelta(seconds=3)),
        )
        info = await environment.client.describe_flow(timeout_id)
        assert await environment.client.invoke_rpc(
            timeout_flow.get_flow_started_timestamp, timeout_id
        ) == int(info.started_at.timestamp())
        result = await environment.client.wait_for_flow(
            timeout_id, timedelta(seconds=30)
        )
        assert result.status is FlowStatus.COMPLETED
        _assert_timeout_started_at(timeout_flow, info.started_at)
