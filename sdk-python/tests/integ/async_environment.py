# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import asyncio
import os
import socket
import time
from tempfile import TemporaryDirectory
from typing import Any

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Flow,
    Registry,
    WorkerOptions,
    open_blob_cache,
)


class AsyncDexDevTestEnvironment:
    def __init__(self, *flows: Flow[Any], allow_async_handlers: bool = False) -> None:
        self._server_address = os.environ.get("DEX_SERVER_ADDRESS", "127.0.0.1:8801")
        self._worker_port = _available_port()
        self._worker_address = f"127.0.0.1:{self._worker_port}"
        self._registry = Registry(flows, allow_async_handlers=allow_async_handlers)
        self._cache_directory = TemporaryDirectory(
            prefix="dex-python-async-integration-cache-"
        )
        self._cache = open_blob_cache(
            BlobCacheConfig(
                self._cache_directory.name,
                64 * 1024 * 1024,
            )
        )
        self._worker = AsyncWorker(
            self._registry,
            self._cache,
            WorkerOptions(
                bind_address=self._worker_address,
                server_address=self._server_address,
            ),
        )
        self._worker_task: asyncio.Task[None] | None = None
        self._client: AsyncClient | None = None

    @property
    def client(self) -> AsyncClient:
        if self._client is None:
            raise RuntimeError("AsyncDexDevTestEnvironment is not entered")
        return self._client

    async def __aenter__(self) -> AsyncDexDevTestEnvironment:
        self._worker_task = asyncio.create_task(self._worker.start())
        await _await_worker(self._worker_port, self._worker_task)
        self._client = AsyncClient(
            self._registry,
            self._cache,
            ClientOptions(self._server_address, self._worker.worker_target),
        )
        return self

    async def __aexit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: object,
    ) -> None:
        if self._client is not None:
            await self._client.close()
            self._client = None
        await self._worker.close()
        if self._worker_task is not None:
            try:
                await asyncio.wait_for(self._worker_task, timeout=10)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                self._worker_task.cancel()
        self._cache.close()
        self._cache_directory.cleanup()


async def _await_worker(port: int, worker_task: asyncio.Task[None]) -> None:
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        if worker_task.done():
            error = worker_task.exception()
            if error is not None:
                raise RuntimeError("Python AsyncWorker failed") from error
            raise RuntimeError("Python AsyncWorker stopped before becoming ready")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                return
        except OSError:
            await asyncio.sleep(0.01)
    raise RuntimeError("Python AsyncWorker did not become ready")


def _available_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])
