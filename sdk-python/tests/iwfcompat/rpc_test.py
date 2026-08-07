# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Client

from .dead_end_flow import DeadEndFlow
from .no_state_flow import NoStateFlow
from .rpc_flow import RpcFlow


def compile_locking(client: Client) -> None:
    flow = NoStateFlow()
    client.start_flow(flow, "rpc-lock", None)
    first: int = client.invoke_rpc(flow.increase_counter, "rpc-lock")
    second: int = client.invoke_rpc(flow.get_counter, "rpc-lock")
    del first, second


def compile_functions_and_procedures(client: Client) -> None:
    flow = RpcFlow()
    client.start_flow(flow, "rpc", 0)
    client.invoke_rpc(flow.no_persistence, "rpc")
    one: int = client.invoke_rpc(flow.function_one, "rpc", "input")
    zero: int = client.invoke_rpc(flow.function_zero, "rpc")
    client.invoke_rpc(flow.procedure_one, "rpc", "input")
    client.invoke_rpc(flow.procedure_zero, "rpc")
    read_only: int = client.invoke_rpc(flow.read_only, "rpc", "input")
    client.invoke_rpc(flow.set_data, "rpc", "value")
    data: str = client.invoke_rpc(flow.get_data, "rpc")
    client.invoke_rpc(flow.set_keyword, "rpc", "value")
    keyword: str = client.invoke_rpc(flow.get_keyword, "rpc")
    del one, zero, read_only, data, keyword


def compile_rpc_error_and_channel_size(client: Client) -> None:
    no_state = NoStateFlow()
    dead_end = DeadEndFlow()
    ignored: int = client.invoke_rpc(no_state.fail, "rpc-error", "error")
    published: int = client.invoke_rpc(
        dead_end.publish_internal,
        "channel-size",
    )
    size: int = client.invoke_rpc(dead_end.signal_size, "channel-size")
    del ignored, published, size
