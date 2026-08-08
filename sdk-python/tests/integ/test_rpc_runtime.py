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
from typing import cast

import grpc
import pytest

from dex import DexException, ErrorSubStatus, StopFlowOptions, StopType

from .async_environment import AsyncDexDevTestEnvironment
from .dead_end_flow import DeadEndFlow
from .no_start_flow import NoStartFlow
from .no_state_flow import NoStateFlow
from .rpc_flow import RpcFlow
from .test_basic_runtime import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def test_flow_without_start_step_can_start_from_rpc() -> None:
    asyncio.run(_flow_without_start_step_can_start_from_rpc())


async def _flow_without_start_step_can_start_from_rpc() -> None:
    flow = NoStartFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("no-start")
        await environment.client.start_flow(flow, flow_id, None)
        assert (
            await environment.client.invoke_rpc(flow.invoke, flow_id, "rpc-input")
            == 100
        )
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1


def test_flow_without_steps_can_serve_rpc() -> None:
    asyncio.run(_flow_without_steps_can_serve_rpc())


async def _flow_without_steps_can_serve_rpc() -> None:
    flow = NoStateFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("no-state")
        await environment.client.start_flow(flow, flow_id, None)
        assert (
            await environment.client.invoke_rpc(flow.invoke, flow_id, "rpc-input")
            == 100
        )
        await environment.client.stop_flow(flow_id)


def test_dead_end_flow_can_be_completed_from_rpc() -> None:
    asyncio.run(_dead_end_flow_can_be_completed_from_rpc())


async def _dead_end_flow_can_be_completed_from_rpc() -> None:
    flow = DeadEndFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("dead-end")
        await environment.client.start_flow(flow, flow_id, None)
        assert (
            await environment.client.invoke_rpc(flow.invoke, flow_id, "rpc-input")
            == 100
        )
        assert (
            await environment.client.wait_for_flow(flow_id, type(None), WAIT_TIMEOUT)
            is None
        )


def test_locking_rpc_serializes_successful_updates() -> None:
    asyncio.run(_locking_rpc_serializes_successful_updates())


async def _locking_rpc_serializes_successful_updates() -> None:
    flow = NoStateFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-lock")
        await environment.client.start_flow(flow, flow_id, None)

        async def increase() -> bool:
            try:
                await environment.client.invoke_rpc(flow.increase_counter, flow_id)
                return True
            except DexException as conflict:
                if conflict.code is grpc.StatusCode.ABORTED:
                    return False
                raise

        results = await asyncio.gather(*(increase() for _ in range(100)))
        succeeded = sum(results)
        assert succeeded > 0
        assert await environment.client.invoke_rpc(flow.get_counter, flow_id) == succeeded
        await environment.client.stop_flow(flow_id)


def test_rpc_without_persistence_unblocks_flow() -> None:
    asyncio.run(_rpc_without_persistence_unblocks_flow())


async def _rpc_without_persistence_unblocks_flow() -> None:
    flow = RpcFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-no-persistence")
        await environment.client.start_flow(flow, flow_id, 999)
        await environment.client.invoke_rpc(flow.no_persistence, flow_id)
        assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2


@pytest.mark.parametrize(
    ("method_name", "argument", "expected_value"),
    (
        ("function_one", "rpc-input", "rpc-input"),
        ("function_zero", None, RpcFlow.HARDCODED_VALUE),
        ("procedure_one", "rpc-input", "rpc-input"),
        ("procedure_zero", None, RpcFlow.HARDCODED_VALUE),
    ),
)
def test_rpc_functions_and_procedures(
    method_name: str,
    argument: str | None,
    expected_value: str,
) -> None:
    asyncio.run(
        _rpc_functions_and_procedures(method_name, argument, expected_value)
    )


async def _rpc_functions_and_procedures(
    method_name: str,
    argument: str | None,
    expected_value: str,
) -> None:
    flow = RpcFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id(f"rpc-{method_name}")
        await environment.client.start_flow(flow, flow_id, 999)
        method = getattr(flow, method_name)
        if argument is None:
            output = await environment.client.invoke_rpc(method, flow_id)
        else:
            output = await environment.client.invoke_rpc(method, flow_id, argument)
        if method_name.startswith("function"):
            assert output == RpcFlow.RPC_OUTPUT
        else:
            assert output is None
        await assert_rpc_completion(environment, flow, flow_id, expected_value)


def test_rpc_attribute_round_trip_and_read_only_call() -> None:
    asyncio.run(_rpc_attribute_round_trip_and_read_only_call())


async def _rpc_attribute_round_trip_and_read_only_call() -> None:
    flow = RpcFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-read-only")
        await environment.client.start_flow(flow, flow_id, 999)
        await environment.client.invoke_rpc(flow.set_data, flow_id, "test-value")
        assert await environment.client.invoke_rpc(flow.get_data, flow_id) == "test-value"
        await environment.client.invoke_rpc(flow.set_data, flow_id, cast(str, None))
        assert await environment.client.invoke_rpc(flow.get_data, flow_id) is None
        await environment.client.invoke_rpc(flow.set_keyword, flow_id, "test-value")
        assert (
            await environment.client.invoke_rpc(flow.get_keyword, flow_id)
            == "test-value"
        )
        await environment.client.invoke_rpc(flow.set_keyword, flow_id, cast(str, None))
        assert await environment.client.invoke_rpc(flow.get_keyword, flow_id) is None
        assert (
            await environment.client.invoke_rpc(flow.read_only, flow_id, "rpc-input")
            == RpcFlow.RPC_OUTPUT
        )
        await environment.client.stop_flow(
            flow_id,
            StopFlowOptions(StopType.FAIL, RpcFlow.HARDCODED_VALUE),
        )


def test_rpc_user_error_preserves_worker_details() -> None:
    asyncio.run(_rpc_user_error_preserves_worker_details())


async def _rpc_user_error_preserves_worker_details() -> None:
    flow = NoStateFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-error")
        await environment.client.start_flow(flow, flow_id, None)
        with pytest.raises(DexException) as captured:
            await environment.client.invoke_rpc(flow.fail, flow_id, "this is an error")
        failure = captured.value
        assert failure.code is grpc.StatusCode.FAILED_PRECONDITION
        assert failure.sub_status is ErrorSubStatus.WORKER_API_ERROR
        assert "ValueError" in failure.worker_error_type
        assert "this is an error" in failure.worker_error_detail
        await environment.client.stop_flow(flow_id)


def test_rpc_channel_size_info() -> None:
    asyncio.run(_rpc_channel_size_info())


async def _rpc_channel_size_info() -> None:
    flow = DeadEndFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("channel-size")
        await environment.client.start_flow(flow, flow_id, None)
        await environment.client.invoke_rpc(flow.publish_internal, flow_id)
        assert await environment.client.invoke_rpc(flow.publish_internal, flow_id) == 2
        await environment.client.publish(flow_id, flow.idle_signal, None, None, None)
        assert await environment.client.invoke_rpc(flow.signal_size, flow_id) == 3
        await environment.client.stop_flow(flow_id)


async def assert_rpc_completion(
    environment: AsyncDexDevTestEnvironment,
    flow: RpcFlow,
    flow_id: str,
    expected_value: str,
) -> None:
    assert await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2
    assert await environment.client.get_attribute(flow_id, flow.data) == expected_value
    assert (
        await environment.client.get_attribute(flow_id, flow.keyword) == expected_value
    )
    assert (
        await environment.client.get_attribute(flow_id, flow.integer)
        == RpcFlow.RPC_OUTPUT
    )
