# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
from datetime import timedelta
from typing import Callable

from dex import (
    AsyncClient,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StartFlowOptions,
    force_complete,
    go_to,
    graceful_complete,
)

from .async_environment import AsyncDexDevTestEnvironment
from .basic_flow import BasicFlow
from .shared import unique_id

WAIT_TIMEOUT = timedelta(seconds=30)


def test_async_client_basic_workflow() -> None:
    asyncio.run(_async_client_basic_workflow())


async def _async_client_basic_workflow() -> None:
    flow = BasicFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        flow_id = unique_id("async-basic")
        await environment.client.start_flow(flow, flow_id, 0)
        assert (
            await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        ).single_output(int) == 2


def test_async_client_concurrent_start_and_wait() -> None:
    asyncio.run(_async_client_concurrent_start_and_wait())


async def _async_client_concurrent_start_and_wait() -> None:
    flow = BasicFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        client = environment.client

        async def one(index: int) -> int:
            flow_id = unique_id(f"async-concurrent-{index}")
            await client.start_flow(flow, flow_id, 0)
            result = (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
                int
            )
            assert result == 2
            return result

        results = await asyncio.gather(*(one(index) for index in range(3)))
        assert results == [2, 2, 2]


class AsyncTimeoutHandlerFlow(Flow[None]):
    async def handle_timeout(  # type: ignore[override]
        self,
        context: Context,
    ) -> StepDecision:
        del context
        await asyncio.sleep(0)
        return force_complete("expired")


def test_async_flow_timeout_handler() -> None:
    asyncio.run(_async_flow_timeout_handler())


async def _async_flow_timeout_handler() -> None:
    flow = AsyncTimeoutHandlerFlow()
    async with AsyncDexDevTestEnvironment(
        flow,
        allow_async_handlers=True,
    ) as environment:
        flow_id = unique_id("async-timeout-handler")
        await environment.client.start_flow(
            flow,
            flow_id,
            None,
            StartFlowOptions(timeout=timedelta(seconds=1)),
        )
        assert (
            await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        ).single_output(str) == "expired"


def test_async_worker_async_execute_starts_child() -> None:
    asyncio.run(_async_worker_async_execute_starts_child())


async def _async_worker_async_execute_starts_child() -> None:
    child = BasicFlow()
    parent = ParentStartsChildFlow(lambda: client_holder["client"], child)
    async with AsyncDexDevTestEnvironment(
        parent,
        child,
        allow_async_handlers=True,
    ) as environment:
        client_holder["client"] = environment.client
        flow_id = unique_id("async-parent")
        await environment.client.start_flow(parent, flow_id, 1)
        assert (
            await environment.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
        ).single_output(int) == 1


client_holder: dict[str, AsyncClient] = {}


class StartChild(Step[int]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        child: BasicFlow,
        finish: Finish,
    ) -> None:
        self._client_provider = client_provider
        self._child = child
        self._finish = finish

    async def execute(  # type: ignore[override]
        self, context: Context, input: int
    ) -> StepDecision:
        del context
        client = self._client_provider()
        child_id = unique_id("async-child")
        await client.start_flow(self._child, child_id, 0)
        (await client.wait_for_flow(child_id, WAIT_TIMEOUT)).single_output(int)
        return go_to(Finish, input)


class Finish(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input)


class ParentStartsChildFlow(Flow[int]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        child: BasicFlow,
    ) -> None:
        self.finish = Finish()
        self.start = StartChild(client_provider, child, self.finish)

    def get_flow_type(self) -> str:
        return "AsyncParentStartsChild"

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(self.finish)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
