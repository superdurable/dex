# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import threading
from concurrent.futures import ThreadPoolExecutor
from typing import Iterator

import grpc
import pytest
from google.protobuf import empty_pb2

from dex.async_worker import AsyncWorker
from dex.attribute import Attribute, AttributeIndex, IndexType
from dex.blob_cache import BlobCacheConfig
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Flow, PersistenceSchema, Registry
from dex.worker import Worker
from dex.worker_options import WorkerOptions


class IndexedFlow(Flow[None]):
    status = Attribute(
        "status",
        str,
        AttributeIndex(IndexType.KEYWORD, "PythonWorkerStatus"),
    )

    def get_flow_type(self) -> str:
        return "IndexedFlow"

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.status)


class HardDeleteFlow(Flow[None]):
    api_key = Attribute("api-key", str, hard_delete_on_null=True)

    def get_flow_type(self) -> str:
        return "HardDeleteFlow"

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.api_key)


class MemoryBlobCache:
    config = BlobCacheConfig("memory", 1_024)

    def get(self, _blob_id: str) -> bytes | None:
        return None

    def put(self, _blob_id: str, _payload: bytes) -> bool:
        return True

    def delete(self, _blob_id: str) -> None:
        return None

    def delete_all(self) -> None:
        return None

    def close(self) -> None:
        return None


class SyncService(
    dex_pb2_grpc.FlowServiceServicer, dex_pb2_grpc.WorkerTaskServiceServicer
):
    def __init__(
        self,
        failure: bool = False,
        server_info: pb.ServerInfo | None = None,
        server_info_failure: grpc.StatusCode | None = None,
    ) -> None:
        self.failure = failure
        self.server_info = (
            server_info
            if server_info is not None
            else pb.ServerInfo(
                server_version="test",
                minimum_supported_protocol_version=1,
                current_protocol_version=1,
            )
        )
        self.server_info_failure = server_info_failure
        self.received: pb.SyncAttributeIndexRequest | None = None
        self.called = threading.Event()
        self.calls: list[str] = []
        self.poll_calls: list[str] = []

    def GetServerInfo(  # noqa: N802
        self,
        request: empty_pb2.Empty,
        context: grpc.ServicerContext,
    ) -> pb.ServerInfo:
        self.calls.append("GetServerInfo")
        if self.server_info_failure is not None:
            context.abort(self.server_info_failure, "server info failed")
        return self.server_info

    def SyncAttributeIndexes(  # noqa: N802
        self,
        request: pb.SyncAttributeIndexRequest,
        context: grpc.ServicerContext,
    ) -> pb.SyncAttributeIndexResponse:
        self.calls.append("SyncAttributeIndexes")
        self.received = request
        self.called.set()
        if self.failure:
            context.abort(grpc.StatusCode.PERMISSION_DENIED, "denied")
        return pb.SyncAttributeIndexResponse()

    def PollWorkerTask(  # noqa: N802
        self,
        request_iterator: Iterator[pb.PollWorkerTaskRequest],
        context: grpc.ServicerContext,
    ) -> Iterator[pb.PollWorkerTaskResponse]:
        self.poll_calls.append("PollWorkerTask")
        assert self.received is not None
        next(request_iterator)
        yield pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered())
        for request in request_iterator:
            pass


def test_worker_synchronizes_indexes_before_polling() -> None:
    service = SyncService()
    flow_server, flow_port = _start_flow_server(service)
    worker = Worker(
        Registry((IndexedFlow(),)),
        MemoryBlobCache(),
        WorkerOptions(
            server_address=f"127.0.0.1:{flow_port}",
        ),
    )
    failures: list[BaseException] = []

    def run_worker() -> None:
        try:
            worker.start()
        except BaseException as failure:
            failures.append(failure)

    thread = threading.Thread(target=run_worker)
    thread.start()
    try:
        assert service.called.wait(timeout=5)
        assert service.received is not None
        assert (
            service.received.attribute_indexes["PythonWorkerStatus"]
            == pb.INDEX_TYPE_KEYWORD
        )
        assert service.calls[:2] == ["GetServerInfo", "SyncAttributeIndexes"]
        worker.wait_pull_registered(timeout=5)
    finally:
        worker.stop()
        thread.join(timeout=5)
        flow_server.stop(grace=None).wait(timeout=5)
    assert not thread.is_alive()
    assert failures == []


def test_worker_sync_failure_prevents_polling() -> None:
    service = SyncService(failure=True)
    flow_server, flow_port = _start_flow_server(service)
    worker = Worker(
        Registry((IndexedFlow(),)),
        MemoryBlobCache(),
        WorkerOptions(
            server_address=f"127.0.0.1:{flow_port}",
        ),
    )
    try:
        with pytest.raises(RuntimeError, match="synchronize Attribute indexes"):
            worker.start()
        assert service.poll_calls == []
    finally:
        worker.close()
        flow_server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize(
    ("server_info", "server_info_failure", "message"),
    (
        (
            pb.ServerInfo(
                server_version="future",
                minimum_supported_protocol_version=4,
                current_protocol_version=4,
            ),
            None,
            "do not overlap",
        ),
        (pb.ServerInfo(server_version="invalid"), None, "interval is invalid"),
        (
            pb.ServerInfo(
                server_version="invalid",
                minimum_supported_protocol_version=2,
                current_protocol_version=1,
            ),
            None,
            "interval is invalid",
        ),
        (None, grpc.StatusCode.UNIMPLEMENTED, "GetServerInfo failed"),
        (None, grpc.StatusCode.UNAVAILABLE, "GetServerInfo failed"),
    ),
)
def test_worker_protocol_failure_precedes_sync_and_polling(
    server_info: pb.ServerInfo | None,
    server_info_failure: grpc.StatusCode | None,
    message: str,
) -> None:
    service = SyncService(
        server_info=server_info,
        server_info_failure=server_info_failure,
    )
    flow_server, flow_port = _start_flow_server(service)
    worker = Worker(
        Registry((IndexedFlow(),)),
        MemoryBlobCache(),
        WorkerOptions(
            server_address=f"127.0.0.1:{flow_port}",
        ),
    )
    try:
        with pytest.raises(RuntimeError, match=message):
            worker.start()
        assert service.calls == ["GetServerInfo"]
        assert service.poll_calls == []
    finally:
        worker.close()
        flow_server.stop(grace=None).wait(timeout=5)


PROTOCOL_2_SERVER_INFO = pb.ServerInfo(
    server_version="previous",
    minimum_supported_protocol_version=1,
    current_protocol_version=2,
)


@pytest.mark.parametrize("flow_type", (IndexedFlow, HardDeleteFlow))
def test_worker_starts_on_protocol_2_server_and_records_version(
    flow_type: type[Flow[None]],
) -> None:
    service = SyncService(server_info=PROTOCOL_2_SERVER_INFO)
    flow_server, flow_port = _start_flow_server(service)
    worker = Worker(
        Registry((flow_type(),)),
        MemoryBlobCache(),
        WorkerOptions(server_address=f"127.0.0.1:{flow_port}"),
    )
    failures: list[BaseException] = []

    def run_worker() -> None:
        try:
            worker.start()
        except BaseException as failure:
            failures.append(failure)

    thread = threading.Thread(target=run_worker)
    thread.start()
    try:
        assert service.called.wait(timeout=5)
        worker.wait_pull_registered(timeout=5)
        assert service.calls[:2] == ["GetServerInfo", "SyncAttributeIndexes"]
        assert worker._server_protocol.version == 2
    finally:
        worker.stop()
        thread.join(timeout=10)
        flow_server.stop(grace=None).wait(timeout=5)
    assert not thread.is_alive()
    assert failures == []


def test_async_worker_with_hard_delete_attributes_starts_on_protocol_2_server() -> None:
    asyncio.run(_test_async_worker_starts_on_protocol_2_server())


async def _test_async_worker_starts_on_protocol_2_server() -> None:
    service = SyncService(server_info=PROTOCOL_2_SERVER_INFO)
    flow_server, flow_port = _start_flow_server(service)
    worker = AsyncWorker(
        Registry((HardDeleteFlow(),), allow_async_handlers=True),
        MemoryBlobCache(),
        WorkerOptions(
            server_address=f"127.0.0.1:{flow_port}",
        ),
    )
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=5)
        assert service.calls[:2] == ["GetServerInfo", "SyncAttributeIndexes"]
        assert worker._server_protocol.version == 2
    finally:
        await worker.stop()
        await asyncio.wait_for(worker_task, timeout=5)
        flow_server.stop(grace=None).wait(timeout=5)


def test_async_worker_synchronizes_indexes_before_polling() -> None:
    asyncio.run(_test_async_worker_synchronizes_indexes_before_polling())


async def _test_async_worker_synchronizes_indexes_before_polling() -> None:
    service = SyncService()
    flow_server, flow_port = _start_flow_server(service)
    worker = AsyncWorker(
        Registry((IndexedFlow(),), allow_async_handlers=True),
        MemoryBlobCache(),
        WorkerOptions(
            server_address=f"127.0.0.1:{flow_port}",
        ),
    )
    worker_task = asyncio.create_task(worker.start())
    try:
        called = await asyncio.to_thread(service.called.wait, 5)
        assert called
        await worker.wait_pull_registered(timeout=5)
    finally:
        await worker.stop()
        await asyncio.wait_for(worker_task, timeout=5)
        flow_server.stop(grace=None).wait(timeout=5)


def _start_flow_server(service: SyncService) -> tuple[grpc.Server, int]:
    server = grpc.server(ThreadPoolExecutor(max_workers=6))
    dex_pb2_grpc.add_FlowServiceServicer_to_server(  # type: ignore[no-untyped-call]
        service,
        server,
    )
    dex_pb2_grpc.add_WorkerTaskServiceServicer_to_server(  # type: ignore[no-untyped-call]
        service, server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    if port == 0:
        raise RuntimeError("cannot bind test FlowService")
    server.start()
    return server, port
