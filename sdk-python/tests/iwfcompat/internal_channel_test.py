# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client

from . import iwf_flows


def compile_basic_internal_channel(client: Client) -> None:
    client.start_flow(iwf_flows.BASIC_INTERNAL, "basic-internal", 1)
    output: int = client.wait_for_flow("basic-internal", int)
    del output


def compile_waiting_internal_channel(client: Client) -> None:
    flow = iwf_flows.WAITING_INTERNAL
    client.start_flow(flow, "waiting-internal", 1)
    client.publish("waiting-internal", flow.channel, 2, 3)
    output: int = client.wait_for_flow("waiting-internal", int)
    del output
