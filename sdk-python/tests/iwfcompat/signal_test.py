# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Client, StepExecutionId, TimerId

from . import iwf_flows


def compile_signals_and_timer_skip(client: Client) -> None:
    flow = iwf_flows.SIGNAL
    client.start_flow(flow, "signal", 0)
    client.publish("signal", flow.first, 1)
    client.publish("signal", flow.second, 2)
    client.publish("signal", flow.third, 3, 4)
    client.publish("signal", flow.signal_map, "one", 5)
    client.skip_timer(
        "signal",
        StepExecutionId("SignalCombinationStep"),
        TimerId.by_condition_id("test-timer-id"),
    )
    output: int = client.wait_for_flow("signal", int)
    del output
