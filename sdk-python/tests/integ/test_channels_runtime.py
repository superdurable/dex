# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import asyncio
from datetime import timedelta

import pytest

from dex import FlowNotActiveError, StepExecutionId, TimerId

from .async_environment import AsyncDexDevTestEnvironment
from .basic_internal_channel_flow import BasicInternalChannelFlow
from .conditional_complete_flow import ConditionalCompleteFlow
from .signal_flow import SignalFlow
from .shared import unique_id
from .waiting_internal_channel_flow import WaitingInternalChannelFlow

WAIT_TIMEOUT = timedelta(seconds=30)


def test_conditional_complete_with_signal_channel() -> None:
    asyncio.run(_conditional_complete_with_signal_channel())


async def _conditional_complete_with_signal_channel() -> None:
    flow = ConditionalCompleteFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("conditional-signal")
        await environment.client.start_flow(flow, flow_id, True)
        await environment.client.publish(flow_id, flow.signal, None)
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1


def test_conditional_complete_with_internal_channel() -> None:
    asyncio.run(_conditional_complete_with_internal_channel())


async def _conditional_complete_with_internal_channel() -> None:
    flow = ConditionalCompleteFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("conditional-internal")
        await environment.client.start_flow(flow, flow_id, False)
        await environment.client.invoke_rpc(flow.publish_to_internal_channel, flow_id)
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1


def test_basic_internal_channel() -> None:
    asyncio.run(_basic_internal_channel())


async def _basic_internal_channel() -> None:
    flow = BasicInternalChannelFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("basic-internal")
        await environment.client.start_flow(flow, flow_id, 1)
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2


def test_waiting_internal_channel() -> None:
    asyncio.run(_waiting_internal_channel())


async def _waiting_internal_channel() -> None:
    flow = WaitingInternalChannelFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("waiting-internal")
        await environment.client.start_flow(flow, flow_id, 1)
        await environment.client.publish(flow_id, flow.channel, 2, 3)
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 6


def test_signal_conditions_and_timer_skip() -> None:
    asyncio.run(_signal_conditions_and_timer_skip())


async def _signal_conditions_and_timer_skip() -> None:
    flow = SignalFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("basic-signal")
        await environment.client.start_flow(flow, flow_id, 1)
        await environment.client.publish(flow_id, flow.first, 2, 3, 5)
        await environment.client.publish(flow_id, flow.third, None)
        await environment.client.publish(flow_id, flow.signal_map, "one", 4)
        await environment.client.skip_timer(
            flow_id,
            StepExecutionId("SignalCombinationStep"),
            TimerId.by_condition_id("test-timer-id"),
        )
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 6
        with pytest.raises(FlowNotActiveError):
            await environment.client.publish(flow_id, flow.first, 8)
