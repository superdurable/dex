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
from datetime import timedelta

import pytest

from dex import Attribute, AttributeMatch, LongPollTimeoutError

from .async_environment import AsyncDexDevTestEnvironment
from .set_attributes_flow import SetAttributesFlow
from .shared import ModelInput, unique_id


def test_async_wait_for_attribute_match() -> None:
    asyncio.run(_async_wait_for_attribute_match())


async def _async_wait_for_attribute_match() -> None:
    flow = SetAttributesFlow()
    timeout = timedelta(seconds=30)
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("async-wait-for-attribute")
        await environment.client.start_flow(flow, flow_id, "start")
        with pytest.raises(LongPollTimeoutError):
            await environment.client.wait_for_attribute_match(
                flow_id,
                flow.data,
                AttributeMatch.equal_to("never"),
                timedelta(seconds=1),
            )
        waiting = asyncio.create_task(
            environment.client.wait_for_attribute_match(
                flow_id, flow.data, AttributeMatch.equal_to("ready"), timeout
            )
        )
        await environment.client.invoke_rpc(flow.set_data, flow_id, "ready")
        assert await waiting == "ready"
        waiting_map = asyncio.create_task(
            environment.client.wait_for_attribute_match(
                flow_id,
                flow.data_map,
                "special % key",
                AttributeMatch.equal_to("mapped"),
                timeout,
            )
        )
        await environment.client.invoke_rpc(flow.set_map_special, flow_id, "mapped")
        assert await waiting_map == "mapped"
        with pytest.raises(
            ValueError,
            match="supports only string, boolean, integer, or float operands",
        ):
            await environment.client.wait_for_attribute_match(
                flow_id,
                flow.model,
                AttributeMatch.equal_to(ModelInput(value=1)),
                timeout,
            )
        with pytest.raises(
            ValueError,
            match="supports only string, boolean, integer, or float operands",
        ):
            await environment.client.wait_for_attribute_match(
                flow_id,
                Attribute("bytes", bytes),
                AttributeMatch.equal_to(b"value"),
                timeout,
            )
        with pytest.raises(
            ValueError,
            match="supports only string, boolean, integer, or float operands",
        ):
            await environment.client.wait_for_attribute_match(
                flow_id,
                Attribute("null", type(None)),
                AttributeMatch.equal_to(None),
                timeout,
            )
