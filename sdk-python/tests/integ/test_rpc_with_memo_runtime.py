# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta
from typing import cast

from dex import StopFlowOptions, StopType

from .environment import DexDevTestEnvironment
from .rpc_flow import RpcFlow
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def test_rpc_memo_function_one() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_flow(environment, flow, "rpc-attribute-func-1")
        environment.client.invoke_rpc(flow.set_data, flow_id, "test-value")
        assert environment.client.invoke_rpc(flow.get_data, flow_id) == "test-value"
        environment.client.invoke_rpc(flow.set_data, flow_id, cast(str, None))
        assert environment.client.invoke_rpc(flow.get_data, flow_id) is None
        environment.client.invoke_rpc(flow.set_keyword, flow_id, "test-value")
        assert environment.client.invoke_rpc(flow.get_keyword, flow_id) == "test-value"
        environment.client.invoke_rpc(flow.set_keyword, flow_id, cast(str, None))
        assert environment.client.invoke_rpc(flow.get_keyword, flow_id) is None
        assert (
            environment.client.invoke_rpc(flow.function_one, flow_id, "rpc-input")
            == RpcFlow.RPC_OUTPUT
        )
        assert_rpc_completion(environment, flow, flow_id, "rpc-input")


def test_rpc_memo_function_zero() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_flow(environment, flow, "rpc-attribute-func-0")
        assert (
            environment.client.invoke_rpc(flow.function_zero, flow_id)
            == RpcFlow.RPC_OUTPUT
        )
        assert_rpc_completion(environment, flow, flow_id, RpcFlow.HARDCODED_VALUE)


def test_rpc_memo_procedure_one() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_flow(environment, flow, "rpc-attribute-proc-1")
        environment.client.invoke_rpc(flow.procedure_one, flow_id, "rpc-input")
        assert_rpc_completion(environment, flow, flow_id, "rpc-input")


def test_rpc_memo_procedure_zero() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_flow(environment, flow, "rpc-attribute-proc-0")
        environment.client.invoke_rpc(flow.procedure_zero, flow_id)
        assert_rpc_completion(environment, flow, flow_id, RpcFlow.HARDCODED_VALUE)


def test_rpc_memo_read_only() -> None:
    flow = RpcFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = start_flow(environment, flow, "rpc-attribute-read-only")
        assert (
            environment.client.invoke_rpc(flow.read_only, flow_id, "rpc-input")
            == RpcFlow.RPC_OUTPUT
        )
        environment.client.stop_flow(
            flow_id,
            StopFlowOptions(StopType.FAIL, RpcFlow.HARDCODED_VALUE),
        )


def start_flow(
    environment: DexDevTestEnvironment,
    flow: RpcFlow,
    prefix: str,
) -> str:
    flow_id = unique_id(prefix)
    environment.client.start_flow(flow, flow_id, 999)
    return flow_id


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
