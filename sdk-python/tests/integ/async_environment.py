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
            WorkerOptions(server_address=self._server_address),
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
        await self._worker.wait_pull_registered(timeout=30)
        self._client = AsyncClient(
            self._registry,
            self._cache,
            ClientOptions(self._server_address),
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
