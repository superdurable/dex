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

from dex import FlowResult, FlowStatus, StopFlowOptions, StopType

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .shared import unique_id
from .stop_complete_flow import StopCompleteAsyncFlow, StopCompleteFlow

STOP_OUTPUT = "completed by StopFlow"


def test_force_complete_cancels_active_step() -> None:
    flow = StopCompleteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-force-complete")
        environment.client.start_flow(flow, flow_id, None)
        assert flow.execute_started.wait(timeout=10)

        environment.client.stop_flow(
            flow_id,
            StopFlowOptions(type=StopType.FORCE_COMPLETE, output=STOP_OUTPUT),
        )

        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=15))
        flow.release_execute.set()
        assert_only_stop_output(result)


def test_graceful_complete_waits_for_active_step() -> None:
    flow = StopCompleteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-graceful-complete")
        environment.client.start_flow(flow, flow_id, None)
        assert flow.execute_started.wait(timeout=10)

        environment.client.stop_flow(
            flow_id,
            StopFlowOptions(type=StopType.GRACEFUL_COMPLETE, output=STOP_OUTPUT),
        )

        assert environment.client.describe_flow(flow_id).status is FlowStatus.RUNNING
        flow.release_execute.set()
        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=15))
        assert_only_stop_output(result)


def test_output_with_cancel_is_rejected_before_sending() -> None:
    flow = StopCompleteFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("python-cancel-output")
        environment.client.start_flow(flow, flow_id, None)
        assert flow.execute_started.wait(timeout=10)

        with pytest.raises(ValueError, match="StopFlowOptions.output requires"):
            environment.client.stop_flow(
                flow_id,
                StopFlowOptions(type=StopType.CANCEL, output=STOP_OUTPUT),
            )

        assert environment.client.describe_flow(flow_id).status is FlowStatus.RUNNING
        flow.release_execute.set()


def test_async_client_graceful_complete() -> None:
    asyncio.run(_run_async_client_graceful_complete())


async def _run_async_client_graceful_complete() -> None:
    flow = StopCompleteAsyncFlow()
    async with AsyncDexDevTestEnvironment(
        flow, allow_async_handlers=True
    ) as environment:
        flow_id = unique_id("python-async-graceful-complete")
        await environment.client.start_flow(flow, flow_id, None)
        await asyncio.wait_for(flow.execute_started.wait(), timeout=10)

        with pytest.raises(ValueError, match="StopFlowOptions.output requires"):
            await environment.client.stop_flow(
                flow_id,
                StopFlowOptions(type=StopType.TERMINATE, output=STOP_OUTPUT),
            )
        await environment.client.stop_flow(
            flow_id,
            StopFlowOptions(type=StopType.GRACEFUL_COMPLETE, output=STOP_OUTPUT),
        )

        info = await environment.client.describe_flow(flow_id)
        assert info.status is FlowStatus.RUNNING
        flow.release_execute.set()
        result = await environment.client.wait_for_flow(flow_id, timedelta(seconds=15))
        assert_only_stop_output(result)


def assert_only_stop_output(result: FlowResult) -> None:
    assert result.status is FlowStatus.COMPLETED
    assert len(result.completions) == 1
    completion = result.completions[0]
    assert completion.step_type == ""
    assert completion.step_execution_id == ""
    assert completion.decode(str) == STOP_OUTPUT
