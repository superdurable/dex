# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
import os
from collections.abc import Awaitable, Callable
from datetime import timedelta
from tempfile import TemporaryDirectory
from typing import Any

import pytest

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Context,
    Flow,
    PullWorkerOptions,
    Registry,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    WorkerOptions,
    graceful_complete,
    open_blob_cache,
)

from .shared import unique_id


class StartupStep(Step[str]):
    def __init__(self, worker_label: str, observations: list[str]) -> None:
        self.worker_label = worker_label
        self.observations = observations

    def execute(self, context: Context, input: str) -> StepDecision:
        self.observations.append(self.worker_label)
        return graceful_complete(input)


class StartupFlow(Flow[str]):
    def __init__(self, worker_label: str, observations: list[str]) -> None:
        self.step = StartupStep(worker_label, observations)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.step)


class StartupResponseGate:
    def __init__(self, call: Callable[..., Awaitable[Any]]) -> None:
        self.call = call
        self.received = asyncio.Event()
        self.release = asyncio.Event()

    async def __call__(self, *args: Any, **kwargs: Any) -> Any:
        response = await self.call(*args, **kwargs)
        self.received.set()
        await self.release.wait()
        return response


@pytest.mark.parametrize("rpc_name", ["GetServerInfo", "SyncAttributeIndexes"])
@pytest.mark.parametrize("shutdown", ["stop", "close"])
def test_worker_stopped_during_start_leaves_tasks_for_another_worker(
    rpc_name: str, shutdown: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    asyncio.run(_stopped_worker_leaves_tasks(rpc_name, shutdown, monkeypatch))


async def _stopped_worker_leaves_tasks(
    rpc_name: str, shutdown: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    server_address = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
    task_queue = unique_id("python-startup")
    observations: list[str] = []
    stopped_flow = StartupFlow("stopped", observations)
    replacement_flow = StartupFlow("replacement", observations)
    registry = Registry([stopped_flow], allow_async_handlers=True)
    options = WorkerOptions(
        server_address=server_address,
        pull=PullWorkerOptions(max_concurrent_tasks=2, task_queue=task_queue),
    )
    with TemporaryDirectory(prefix="dex-python-startup-") as cache_directory:
        cache = open_blob_cache(BlobCacheConfig(cache_directory, 16 * 1024 * 1024))
        worker = AsyncWorker(registry, cache, options)
        replacement = AsyncWorker(
            Registry([replacement_flow], allow_async_handlers=True), cache, options
        )
        client = AsyncClient(registry, cache, ClientOptions(server_address))
        gate = StartupResponseGate(getattr(worker._flow_service, rpc_name))
        monkeypatch.setattr(worker._flow_service, rpc_name, gate)
        worker_task: asyncio.Task[None] | None = None
        replacement_task: asyncio.Task[None] | None = None
        readiness: asyncio.Task[None] | None = None
        try:
            flow_id = unique_id("python-startup-flow")
            await client.start_flow(
                stopped_flow, flow_id, "done", StartFlowOptions(task_queue=task_queue)
            )
            worker_task = asyncio.create_task(worker.start())
            readiness = asyncio.create_task(worker.wait_pull_registered(timeout=10))
            await asyncio.wait_for(gate.received.wait(), timeout=10)
            await asyncio.wait_for(getattr(worker, shutdown)(), timeout=10)
            with pytest.raises(RuntimeError, match="stopped"):
                await readiness
            gate.release.set()
            await asyncio.wait_for(worker_task, timeout=10)
            assert worker._state == ("closed" if shutdown == "close" else "stopped")
            assert worker.pull_slot_stream_state().open == 0
            assert observations == []

            replacement_task = asyncio.create_task(replacement.start())
            await replacement.wait_pull_registered(timeout=10)
            result = await client.wait_for_flow(flow_id, timedelta(seconds=20))
            assert result.single_output(str) == "done"
            assert observations == ["replacement"]
        finally:
            gate.release.set()
            await worker.close()
            await replacement.close()
            for task in (worker_task, replacement_task, readiness):
                if task is not None:
                    if not task.done():
                        task.cancel()
                    await asyncio.gather(task, return_exceptions=True)
            await client.close()
            cache.close()
