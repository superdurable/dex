# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import asyncio
from datetime import timedelta
from typing import Callable
from uuid import uuid4

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
    go_to,
)

from .async_environment import AsyncDexDevTestEnvironment
from .basic_flow import BasicFlow

WAIT_TIMEOUT = timedelta(seconds=30)


def unique_id(prefix: str) -> str:
    return f"{prefix}-{uuid4()}"


def test_async_client_basic_workflow() -> None:
    asyncio.run(_async_client_basic_workflow())


async def _async_client_basic_workflow() -> None:
    flow = BasicFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        assert environment.client is not None
        flow_id = unique_id("async-basic")
        await environment.client.start_flow(flow, flow_id, 0)
        assert (
            await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 2
        )


def test_async_client_concurrent_start_and_wait() -> None:
    asyncio.run(_async_client_concurrent_start_and_wait())


async def _async_client_concurrent_start_and_wait() -> None:
    flow = BasicFlow()
    async with AsyncDexDevTestEnvironment(flow) as environment:
        assert environment.client is not None
        client = environment.client

        async def one(index: int) -> int:
            flow_id = unique_id(f"async-concurrent-{index}")
            await client.start_flow(flow, flow_id, 0)
            result = await client.wait_for_flow(flow_id, int, WAIT_TIMEOUT)
            assert result == 2
            return result

        results = await asyncio.gather(*(one(index) for index in range(3)))
        assert results == [2, 2, 2]


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
        assert environment.client is not None
        client_holder["client"] = environment.client
        flow_id = unique_id("async-parent")
        await environment.client.start_flow(parent, flow_id, 1)
        assert (
            await environment.client.wait_for_flow(flow_id, int, WAIT_TIMEOUT) == 1
        )


client_holder: dict[str, object] = {}


class StartChild(Step[int]):
    def __init__(
        self,
        client_provider: Callable[[], object],
        child: BasicFlow,
        finish: Finish,
    ) -> None:
        self._client_provider = client_provider
        self._child = child
        self._finish = finish

    async def execute(self, context: Context, input: int) -> StepDecision:
        del context
        client = self._client_provider()
        child_id = unique_id("async-child")
        await client.start_flow(self._child, child_id, 0)  # type: ignore[union-attr]
        await client.wait_for_flow(child_id, int, WAIT_TIMEOUT)  # type: ignore[union-attr]
        return go_to(self._finish, input)


class Finish(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input)


class ParentStartsChildFlow(Flow[int]):
    def __init__(
        self,
        client_provider: Callable[[], object],
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
