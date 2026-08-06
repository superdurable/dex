# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_signal_channel(client: Client) -> None:
    flow = iwf_flows.CONDITIONAL_COMPLETE
    client.start_flow(flow, "conditional-signal", True)
    client.publish("conditional-signal", flow.signal, None)
    output: int = client.wait_for_flow("conditional-signal", int)
    del output


def compile_internal_channel(client: Client) -> None:
    flow = iwf_flows.CONDITIONAL_COMPLETE
    client.start_flow(flow, "conditional-internal", False)
    client.invoke_rpc(
        flow.publish_to_internal_channel,
        "conditional-internal",
    )
