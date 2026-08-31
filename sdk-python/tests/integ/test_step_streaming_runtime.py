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

from .environment import DexDevTestEnvironment
from .shared import unique_id
from .step_streaming_flow import StepStreamingFlow


def test_step_streaming_restores_heartbeat_values_across_retries() -> None:
    flow = StepStreamingFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("step-streaming")
        environment.client.start_flow(flow, flow_id, None)
        result = environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert result.single_output(str) == "recovered"

        messages: list[tuple[str, str]] = []
        resume_token = ""
        for _ in range(4):
            message = environment.client.read_stream(
                flow_id,
                flow.progress,
                resume_token,
                timedelta(seconds=30),
            )
            messages.append((message.value, message.source))
            resume_token = message.resume_token

        assert [value for value, _ in messages] == [
            "wait-stream",
            "attempt-1-first",
            "attempt-1-second",
            "attempt-2-after-clear",
        ]
        assert {source for _, source in messages} == {"#StepStreamingStep-1"}
