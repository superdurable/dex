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

from .async_environment import AsyncDexDevTestEnvironment
from .environment import DexDevTestEnvironment
from .multi_output_flow import MultiOutputFlow
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)

def test_sync_multi_output_flow() -> None:
    flow = MultiOutputFlow()
    with DexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("multi-output")
        environment.client.start_flow(flow, flow_id, None)
        result = environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        outputs = {
            completion.step_type: completion for completion in result.completions
        }
        assert len(outputs) == 2
        assert outputs[flow.string_step.get_step_type()].decode(str) == "branch-one"
        assert outputs[flow.int_step.get_step_type()].decode(int) == 42
        assert all(completion.step_execution_id for completion in result.completions)

def test_async_multi_output_flow() -> None:
    asyncio.run(_test_async_multi_output_flow())

async def _test_async_multi_output_flow() -> None:
    flow = MultiOutputFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("async-multi-output")
        await environment.client.start_flow(flow, flow_id, None)
        result = await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        completions = {
            completion.step_type: completion for completion in result.completions
        }
        assert completions[flow.string_step.get_step_type()].decode(str) == "branch-one"
        assert completions[flow.int_step.get_step_type()].decode(int) == 42
