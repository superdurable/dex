# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import asyncio
from datetime import datetime, timedelta, timezone

from .async_environment import AsyncDexDevTestEnvironment
from .async_stream_flow import AsyncStreamTestFlow
from .shared import unique_id


def test_async_stream_round_trip() -> None:
    asyncio.run(_async_stream_round_trip())


async def _async_stream_round_trip() -> None:
    flow = AsyncStreamTestFlow()
    async with AsyncDexDevTestEnvironment(
        flow,
        allow_async_handlers=True,
    ) as environment:
        flow_id = unique_id("async-stream")
        run_id = await environment.client.start_flow(flow, flow_id, None)
        await environment.client.wait_for_flow(flow_id, timedelta(seconds=30))

        await environment.client.write_stream(
            flow_id,
            flow.progress,
            "async-client-write",
            "async-client-progress",
        )

        step = await environment.client.read_stream(
            flow_id,
            flow.progress,
            timeout=timedelta(seconds=30),
        )
        assert step.value == "async-step-progress"
        assert step.resume_token
        assert step.created_time > datetime(1970, 1, 1, tzinfo=timezone.utc)
        assert step.idempotency_key.startswith(f"{run_id}#")

        client = await environment.client.read_stream(
            flow_id,
            flow.progress,
            step.resume_token,
            timeout=timedelta(seconds=30),
        )
        assert client.value == "async-client-progress"
        assert client.resume_token != step.resume_token
        assert client.created_time > datetime(1970, 1, 1, tzinfo=timezone.utc)
        assert client.idempotency_key == "async-client-write"
