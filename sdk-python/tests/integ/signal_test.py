# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Client, StepExecutionId, TimerId

from .signal_flow import SignalFlow


def compile_signals_and_timer_skip(client: Client) -> None:
    flow = SignalFlow()
    client.start_flow(flow, "signal", 0)
    client.invoke_rpc(flow.publish_first, "signal", 1)
    client.invoke_rpc(flow.publish_second, "signal", 2)
    client.invoke_rpc(flow.publish_third, "signal")
    client.invoke_rpc(flow.publish_mapped, "signal", 5)
    client.skip_timer(
        "signal",
        StepExecutionId("SignalCombinationStep"),
        TimerId.by_condition_id("test-timer-id"),
    )
    output: int = client.wait_for_flow("signal").single_output(int)
    del output
