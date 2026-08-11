# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import asyncio
from types import TracebackType

import grpc

from dex._async_value_hydrator import AsyncValueHydrator
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._async_worker_service import AsyncWorkerService
from dex._value_mapper import ValueMapper
from dex.blob_cache import BlobCache
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Registry
from dex.worker_options import WorkerOptions, WorkerTarget


class AsyncWorker:
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions | None = None,
    ) -> None:
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or WorkerOptions()
        self._state = "created"
        self._flow_channel = grpc.aio.insecure_channel(self.options.server_address)
        self._flow_service = dex_pb2_grpc.FlowServiceStub(  # type: ignore[no-untyped-call]
            self._flow_channel
        )
        values = ValueMapper(registry.codec_registry)
        dispatcher = AsyncWorkerDispatcher(
            registry,
            values,
            AsyncValueHydrator(self._flow_service, blob_cache),
        )
        self._server = grpc.aio.server()
        dex_pb2_grpc.add_WorkerServiceServicer_to_server(  # type: ignore[no-untyped-call]
            AsyncWorkerService(dispatcher),
            self._server,
        )
        self._bound_port = 0
        self._worker_target = self.options.worker_target or WorkerTarget(
            self._target_address(self.options.bind_address, 0)
        )
        self._stopped = asyncio.Event()

    @property
    def worker_target(self) -> WorkerTarget:
        return self._worker_target

    async def __aenter__(self) -> AsyncWorker:
        return self

    async def __aexit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        await self.close()

    async def start(self) -> None:
        if self._state != "created":
            raise RuntimeError(f"AsyncWorker cannot start from state {self._state}")
        try:
            await self._flow_service.SyncAttributeIndexes(
                pb.SyncAttributeIndexRequest(
                    attribute_indexes=dict(self.registry._attribute_indexes)
                ),
                timeout=self.options.attribute_index_sync_timeout.total_seconds(),
            )
        except grpc.RpcError as failure:
            self._state = "stopped"
            await self._flow_channel.close()
            self._stopped.set()
            raise RuntimeError("cannot synchronize Attribute indexes") from failure
        self._bound_port = self._server.add_insecure_port(self.options.bind_address)
        if self._bound_port == 0:
            self._state = "stopped"
            await self._flow_channel.close()
            self._stopped.set()
            raise RuntimeError(
                f"cannot bind Python AsyncWorker to {self.options.bind_address}"
            )
        if self.options.worker_target is None:
            self._worker_target = WorkerTarget(
                self._target_address(self.options.bind_address, self._bound_port)
            )
        self._state = "running"
        await self._server.start()
        await self._server.wait_for_termination()
        self._stopped.set()

    async def stop(self) -> None:
        if self._state in ("stopped", "closed"):
            return
        self._state = "stopping"
        await self._server.stop(grace=5)
        await self._flow_channel.close()
        if self._state != "closed":
            self._state = "stopped"
        self._stopped.set()

    async def close(self) -> None:
        await self.stop()
        self._state = "closed"

    @staticmethod
    def _target_address(bind_address: str, bound_port: int) -> str:
        host, separator, port = bind_address.rpartition(":")
        if not separator or not port:
            raise ValueError("Worker bind address requires a port")
        if bound_port == 0:
            bound_port = int(port)
        if host in ("", "0.0.0.0", "::", "[::]"):
            host = "localhost"
        return f"{host}:{bound_port}"
