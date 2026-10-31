# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""One private engine, Client, and set of Worker children per test."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from pathlib import Path
from types import TracebackType
from typing import Any

from dex import (
    AsyncClient,
    BlobCache,
    BlobCacheConfig,
    ClientOptions,
    Flow,
    Registry,
    open_blob_cache,
)

from dex_agents.client import AgentSessionClient
from dex_agents.session_flow import AgentSessionFlow
from dex_agents.testing.history import StepHistoryEntry, read_step_history
from dex_agents.testing.processes import AgentWorkerProcess, PrivateDexEngine
from dex_agents.testing.worker import TEST_BLOB_CACHE_BYTES


class AgentTestStack:
    """Own everything one agent integration test needs, and clean it up.

    Entering the stack starts a ``PrivateDexEngine`` and opens an
    ``AsyncClient`` whose Registry holds ``flows``. ``start_worker`` starts a
    Worker child running ``worker_module``. Leaving the stack SIGKILLs every
    Worker, closes the Client, and kills the engine.

    The test process registers the same Flow types as the Worker child, but
    never runs their Steps: its Flows only type the Client calls. Build them
    with the same Flow type, Stream capacity, and persistence schema.

    Example:
        >>> async with AgentTestStack(tmp_path, [flow], "tests.my_worker",
        ...                           worker_cwd=PACKAGE_DIR) as stack:
        ...     worker = await stack.start_worker()
        ...     sessions = stack.sessions(flow)
        ...     await sessions.start_session("session-1", "hello")
        ...     worker.kill()
        ...     await stack.start_worker()
    """

    def __init__(
        self,
        state_dir: Path,
        flows: Sequence[Flow[Any]],
        worker_module: str,
        *,
        worker_cwd: Path | None = None,
        worker_env: Mapping[str, str] | None = None,
    ) -> None:
        """Describe the stack without starting anything.

        Args:
            state_dir: Directory for engine state, logs, and BlobCaches.
            flows: Flows registered in the test-side Client.
            worker_module: Module each Worker child runs with ``python -m``.
            worker_cwd: Working directory that makes ``worker_module`` importable.
            worker_env: Environment variables for every Worker child.
        """
        self._state_dir = state_dir
        self._flows = tuple(flows)
        self._worker_module = worker_module
        self._worker_cwd = worker_cwd
        self._worker_env = dict(worker_env or {})
        self._engine = PrivateDexEngine(state_dir / "engine")
        self._workers: list[AgentWorkerProcess] = []
        self._blob_cache: BlobCache | None = None
        self._client: AsyncClient | None = None

    @property
    def engine(self) -> PrivateDexEngine:
        """Return the private engine.

        Returns:
            The engine, started while the stack is entered.
        """
        return self._engine

    @property
    def client(self) -> AsyncClient:
        """Return the test-side Client.

        Returns:
            The open Client.

        Raises:
            RuntimeError: Outside the ``async with`` block.
        """
        if self._client is None:
            raise RuntimeError("enter the AgentTestStack before using its Client")
        return self._client

    @property
    def workers(self) -> tuple[AgentWorkerProcess, ...]:
        """Return every Worker child the stack created.

        Returns:
            Workers in creation order, running or killed.
        """
        return tuple(self._workers)

    def sessions(self, flow: AgentSessionFlow) -> AgentSessionClient:
        """Return a typed session Client for one registered agent Flow.

        Args:
            flow: One of the stack's Flows.

        Returns:
            A session Client over the stack's Client.
        """
        return AgentSessionClient(self.client, flow)

    async def read_step_history(self, flow_id: str) -> list[StepHistoryEntry]:
        """Return the completed Steps of a Flow on the stack's engine.

        Args:
            flow_id: Flow ID, such as a session ID.

        Returns:
            Completed ``wait_for`` and ``execute`` methods in history order.
        """
        return await read_step_history(self._engine.address, flow_id)

    async def start_worker(self, extra_env: Mapping[str, str] | None = None) -> AgentWorkerProcess:
        """Start a new Worker child and wait until it serves every pull queue.

        Args:
            extra_env: Environment variables for this Worker only.

        Returns:
            The running Worker. Call ``kill`` on it to model a crash.
        """
        worker = AgentWorkerProcess(
            self._engine.address,
            self._worker_module,
            self._state_dir,
            name=f"agent Worker {len(self._workers) + 1}",
            cwd=self._worker_cwd,
            extra_env={**self._worker_env, **(extra_env or {})},
        )
        self._workers.append(worker)
        await worker.start()
        return worker

    async def __aenter__(self) -> AgentTestStack:
        await self._engine.start()
        try:
            self._blob_cache = open_blob_cache(
                BlobCacheConfig(str(self._state_dir / "client-blob-cache"), TEST_BLOB_CACHE_BYTES)
            )
            self._client = AsyncClient(
                Registry(list(self._flows), allow_async_handlers=True),
                self._blob_cache,
                ClientOptions(server_address=self._engine.address),
            )
        except BaseException:
            self._engine.kill()
            raise
        return self

    async def __aexit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        for worker in self._workers:
            worker.kill()
        try:
            if self._client is not None:
                await self._client.close()
        finally:
            if self._blob_cache is not None:
                self._blob_cache.close()
            self._client = None
            self._blob_cache = None
            self._engine.kill()
