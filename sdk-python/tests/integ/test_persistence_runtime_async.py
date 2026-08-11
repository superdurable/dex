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
from datetime import timedelta

import pytest

from dex import Attribute, LongPollTimeoutError

from .async_environment import AsyncDexDevTestEnvironment
from .set_attributes_flow import SetAttributesFlow
from .shared import ModelInput, unique_id


@pytest.mark.asyncio
async def test_async_wait_for_attribute_equal() -> None:
    flow = SetAttributesFlow()
    timeout = timedelta(seconds=30)
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("async-wait-for-attribute")
        await environment.client.start_flow(flow, flow_id, "start")
        with pytest.raises(LongPollTimeoutError):
            await environment.client.wait_for_attribute_equal(
                flow_id, flow.data, "never", timedelta(seconds=1)
            )
        waiting = asyncio.create_task(
            environment.client.wait_for_attribute_equal(
                flow_id, flow.data, "ready", timeout
            )
        )
        await environment.client.set_attribute(flow_id, flow.data, "ready")
        await waiting
        waiting_map = asyncio.create_task(
            environment.client.wait_for_attribute_map_equal(
                flow_id, flow.data_map, "special / key", "mapped", timeout
            )
        )
        await environment.client.set_attribute(
            flow_id, flow.data_map, "special / key", "mapped"
        )
        await waiting_map
        with pytest.raises(ValueError, match="only scalar"):
            await environment.client.wait_for_attribute_equal(
                flow_id, flow.model, ModelInput(value=1), timeout
            )
        with pytest.raises(ValueError, match="only scalar"):
            await environment.client.wait_for_attribute_equal(
                flow_id, Attribute("bytes", bytes), b"value", timeout
            )
        with pytest.raises(ValueError, match="only scalar"):
            await environment.client.wait_for_attribute_equal(
                flow_id, Attribute("null", type(None)), None, timeout
            )
