# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import datetime, timedelta, timezone

from .environment import DexDevTestEnvironment
from .shared import unique_id
from .stream_flow import StreamTestFlow


def test_stream_round_trip() -> None:
    flow = StreamTestFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("stream")
        environment.client.start_flow(flow, flow_id, None)
        environment.client.wait_for_flow(flow_id, timedelta(seconds=30))

        environment.client.write_stream(
            flow_id, flow.progress, "client#write", "client-progress"
        )
        environment.client.write_stream(
            flow_id, flow.progress, "client#write", "duplicate-retained"
        )

        step = environment.client.read_stream(
            flow_id, flow.progress, timeout=timedelta(seconds=30)
        )
        assert step.value == "step-progress"
        assert step.resume_token
        assert step.created_time > datetime(1970, 1, 1, tzinfo=timezone.utc)
        assert step.source.startswith("#StreamTestStep-")

        client = environment.client.read_stream(
            flow_id,
            flow.progress,
            step.resume_token,
            timeout=timedelta(seconds=30),
        )
        assert client.value == "client-progress"
        assert client.resume_token != step.resume_token
        assert client.created_time > datetime(1970, 1, 1, tzinfo=timezone.utc)
        assert client.source == "client#write"

        duplicate = environment.client.read_stream(
            flow_id,
            flow.progress,
            client.resume_token,
            timeout=timedelta(seconds=30),
        )
        assert duplicate.value == "duplicate-retained"
        assert duplicate.source == "client#write"
