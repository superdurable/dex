# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_no_start_step(client: Client) -> None:
    flow = iwf_flows.NO_START
    client.start_flow(flow, "no-start", None)
    output: int = client.invoke_rpc(flow.invoke, "no-start", "input")
    del output


def compile_no_step(client: Client) -> None:
    flow = iwf_flows.NO_STATE
    client.start_flow(flow, "no-step", None)
    output: int = client.invoke_rpc(flow.increase_counter, "no-step")
    client.stop_flow("no-step")
    del output


def compile_dead_end(client: Client) -> None:
    flow = iwf_flows.DEAD_END
    client.start_flow(flow, "dead-end", None)
    size: int = client.invoke_rpc(flow.publish_internal, "dead-end")
    del size
