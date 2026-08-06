# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import timedelta

from dex import Client, StepExecutionId

from . import iwf_flows


def compile_timer_and_step_wait(client: Client) -> None:
    client.start_flow(iwf_flows.TIMER, "timer", 1)
    client.wait_for_step_completion(
        "timer",
        StepExecutionId("TimerStep"),
        timedelta(seconds=10),
    )
    client.wait_for_flow("timer")
