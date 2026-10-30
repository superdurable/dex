# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

import asyncio
from datetime import datetime, timedelta, timezone

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .json_codec_flow import JsonCodecFlow
from .json_codec_models import (
    CodecEnvelope,
    CodecItem,
    CodecReceipt,
    CodecResult,
)
from .shared import unique_id


def test_generic_json_codec_round_trip_through_sync_worker() -> None:
    flow = JsonCodecFlow()
    input = _codec_envelope()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("json-codec-sync")
        environment.client.start_flow(flow, flow_id, input)
        completed = environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert completed.single_output(CodecEnvelope[CodecItem]) == input


def test_generic_json_codec_round_trip_through_async_worker() -> None:
    asyncio.run(_round_trip_through_async_worker())


async def _round_trip_through_async_worker() -> None:
    flow = JsonCodecFlow()
    input = _codec_envelope()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("json-codec-async")
        await environment.client.start_flow(flow, flow_id, input)
        completed = await environment.client.wait_for_flow(
            flow_id, timedelta(seconds=30)
        )
        assert completed.single_output(CodecEnvelope[CodecItem]) == input


def _codec_envelope() -> CodecEnvelope[CodecItem]:
    result = CodecResult(
        "success",
        CodecItem(42),
        CodecReceipt(
            "call-42", datetime(2026, 10, 30, 7, 0, 0, 120000, tzinfo=timezone.utc)
        ),
    )
    return CodecEnvelope([result], {"result": result})
