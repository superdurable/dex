# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta
from typing import cast

import grpc
import pytest

from dex import DexException, ErrorSubStatus, StopFlowOptions, StopType

from .dead_end_flow import DeadEndFlow
from .environment import DexDevTestEnvironment
from .no_start_flow import NoStartFlow
from .no_state_flow import NoStateFlow
from .rpc_flow import RpcFlow
from .test_basic_runtime import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def test_flow_without_start_step_can_start_from_rpc() -> None:
    flow = NoStartFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("no-start")
        environment.client.start_flow(flow, flow_id, None)
        assert environment.client.invoke_rpc(flow.invoke, flow_id, "rpc-input") == 100
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1


def test_flow_without_steps_can_serve_rpc() -> None:
    flow = NoStateFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("no-state")
        environment.client.start_flow(flow, flow_id, None)
        assert environment.client.invoke_rpc(flow.invoke, flow_id, "rpc-input") == 100
        environment.client.stop_flow(flow_id)


def test_dead_end_flow_can_be_completed_from_rpc() -> None:
    flow = DeadEndFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("dead-end")
        environment.client.start_flow(flow, flow_id, None)
        assert environment.client.invoke_rpc(flow.invoke, flow_id, "rpc-input") == 100
        assert (
            environment.client.wait_for_flow(flow_id, type(None), WAIT_TIMEOUT) is None
        )


def test_locking_rpc_serializes_successful_updates() -> None:
    flow = NoStateFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-lock")
        environment.client.start_flow(flow, flow_id, None)

        def increase() -> bool:
            try:
                environment.client.invoke_rpc(flow.increase_counter, flow_id)
                return True
            except DexException as conflict:
                if conflict.code is grpc.StatusCode.ABORTED:
                    return False
                raise

        with ThreadPoolExecutor(max_workers=10) as executor:
            succeeded = sum(executor.map(lambda _: increase(), range(100)))
        assert succeeded > 0
        assert environment.client.invoke_rpc(flow.get_counter, flow_id) == succeeded
        environment.client.stop_flow(flow_id)


def test_rpc_without_persistence_unblocks_flow() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-no-persistence")
        environment.client.start_flow(flow, flow_id, 999)
        environment.client.invoke_rpc(flow.no_persistence, flow_id)
        assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2


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
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id(f"rpc-{method_name}")
        environment.client.start_flow(flow, flow_id, 999)
        method = getattr(flow, method_name)
        if argument is None:
            output = environment.client.invoke_rpc(method, flow_id)
        else:
            output = environment.client.invoke_rpc(method, flow_id, argument)
        if method_name.startswith("function"):
            assert output == RpcFlow.RPC_OUTPUT
        else:
            assert output is None
        assert_rpc_completion(environment, flow, flow_id, expected_value)


def test_rpc_attribute_round_trip_and_read_only_call() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-read-only")
        environment.client.start_flow(flow, flow_id, 999)
        environment.client.invoke_rpc(flow.set_data, flow_id, "test-value")
        assert environment.client.invoke_rpc(flow.get_data, flow_id) == "test-value"
        environment.client.invoke_rpc(flow.set_data, flow_id, cast(str, None))
        assert environment.client.invoke_rpc(flow.get_data, flow_id) is None
        environment.client.invoke_rpc(flow.set_keyword, flow_id, "test-value")
        assert environment.client.invoke_rpc(flow.get_keyword, flow_id) == "test-value"
        environment.client.invoke_rpc(flow.set_keyword, flow_id, cast(str, None))
        assert environment.client.invoke_rpc(flow.get_keyword, flow_id) is None
        assert (
            environment.client.invoke_rpc(flow.read_only, flow_id, "rpc-input")
            == RpcFlow.RPC_OUTPUT
        )
        environment.client.stop_flow(
            flow_id,
            StopFlowOptions(StopType.FAIL, RpcFlow.HARDCODED_VALUE),
        )


def test_rpc_user_error_preserves_worker_details() -> None:
    flow = NoStateFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("rpc-error")
        environment.client.start_flow(flow, flow_id, None)
        with pytest.raises(DexException) as captured:
            environment.client.invoke_rpc(flow.fail, flow_id, "this is an error")
        failure = captured.value
        assert failure.code is grpc.StatusCode.FAILED_PRECONDITION
        assert failure.sub_status is ErrorSubStatus.WORKER_API_ERROR
        assert "ValueError" in failure.worker_error_type
        assert "this is an error" in failure.worker_error_detail
        environment.client.stop_flow(flow_id)


def test_rpc_channel_size_info() -> None:
    flow = DeadEndFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("channel-size")
        environment.client.start_flow(flow, flow_id, None)
        environment.client.invoke_rpc(flow.publish_internal, flow_id)
        assert environment.client.invoke_rpc(flow.publish_internal, flow_id) == 2
        environment.client.publish(flow_id, flow.idle_signal, None, None, None)
        assert environment.client.invoke_rpc(flow.signal_size, flow_id) == 3
        environment.client.stop_flow(flow_id)


def assert_rpc_completion(
    environment: DexDevTestEnvironment,
    flow: RpcFlow,
    flow_id: str,
    expected_value: str,
) -> None:
    assert environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2
    assert environment.client.get_attribute(flow_id, flow.data) == expected_value
    assert environment.client.get_attribute(flow_id, flow.keyword) == expected_value
    assert environment.client.get_attribute(flow_id, flow.integer) == RpcFlow.RPC_OUTPUT
