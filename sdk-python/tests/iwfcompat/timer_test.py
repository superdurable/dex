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

from dex import Client, StepExecutionId

from .timer_flow import TimerFlow


def compile_timer_and_step_wait(client: Client) -> None:
    client.start_flow(TimerFlow(), "timer", 1)
    client.wait_for_step_completion(
        "timer",
        StepExecutionId("TimerStep"),
        timedelta(seconds=10),
    )
    client.wait_for_flow("timer")
