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
from .no_start_flow import NoStartFlow
from .no_state_flow import NoStateFlow


def compile_no_start_step(client: Client) -> None:
    flow = NoStartFlow()
    client.start_flow(flow, "no-start", None)
    output: int = client.invoke_rpc(flow.invoke, "no-start", "input")
    del output


def compile_no_step(client: Client) -> None:
    flow = NoStateFlow()
    client.start_flow(flow, "no-step", None)
    output: int = client.invoke_rpc(flow.increase_counter, "no-step")
    client.stop_flow("no-step")
    del output


def compile_dead_end(client: Client) -> None:
    flow = DeadEndFlow()
    client.start_flow(flow, "dead-end", None)
    size: int = client.invoke_rpc(flow.publish_internal, "dead-end")
    del size
