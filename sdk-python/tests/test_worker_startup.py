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
from collections.abc import AsyncIterator, Awaitable, Callable
from concurrent.futures import ThreadPoolExecutor
from contextlib import asynccontextmanager
from typing import Any, Iterator, cast

import grpc
import pytest
from google.protobuf import empty_pb2

from dex.async_worker import AsyncWorker
from dex.attribute import Attribute, AttributeIndex, IndexType
from dex.blob_cache import BlobCacheConfig
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Flow, PersistenceSchema, Registry
from dex.pull_registration import PullRegistrationError
from dex.worker import Worker
from dex.worker_options import PullWorkerOptions, WorkerOptions


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

    def get(self, blob_id: str) -> bytes | None:
        return None

    def put(self, blob_id: str, payload: bytes) -> bool:
        return True

    def delete(self, blob_id: str) -> None:
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
        poll_rejection: grpc.StatusCode | None = None,
        delayed_rpc: str | None = None,
    ) -> None:
        self.failure = failure
        self.poll_rejection = poll_rejection
        self.server_info = (
            server_info
            if server_info is not None
            else pb.ServerInfo(
                server_version="test",
                minimum_supported_protocol_version=1,
                current_protocol_version=4,
            )
        )
        self.server_info_failure = server_info_failure
        self.received: pb.SyncAttributeIndexRequest | None = None
        self.called = threading.Event()
        self.calls: list[str] = []
        self.poll_calls: list[str] = []
        self.polling_during_sync = False
        self.delayed_rpc = delayed_rpc
        self.rpc_started = threading.Event()
        self.release_rpc = threading.Event()

    def GetServerInfo(  # noqa: N802
        self,
        request: empty_pb2.Empty,
        context: grpc.ServicerContext,
    ) -> pb.ServerInfo:
        self.calls.append("GetServerInfo")
        self._wait_for_release("GetServerInfo")
        if self.server_info_failure is not None:
            context.abort(self.server_info_failure, "server info failed")
        return self.server_info

    def SyncAttributeIndexes(  # noqa: N802
        self,
        request: pb.SyncAttributeIndexRequest,
        context: grpc.ServicerContext,
    ) -> pb.SyncAttributeIndexResponse:
        self.polling_during_sync = bool(self.poll_calls)
        self.calls.append("SyncAttributeIndexes")
        self._wait_for_release("SyncAttributeIndexes")
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
        next(request_iterator)
        if self.poll_rejection is not None:
            context.abort(self.poll_rejection, "poll stream rejected")
        yield pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered())
        for request in request_iterator:
            pass

    def _wait_for_release(self, rpc_name: str) -> None:
        if self.delayed_rpc == rpc_name:
            self.rpc_started.set()
            if not self.release_rpc.wait(timeout=10):
                raise TimeoutError("startup RPC was not released")


def test_worker_synchronizes_indexes_before_polling() -> None:
    service = SyncService()
    flow_server, flow_port = _start_flow_server(service)
    worker = Worker(
        Registry((IndexedFlow(),)),
        MemoryBlobCache(),
        WorkerOptions(
            pull=PullWorkerOptions(max_concurrent_tasks=2),
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
        assert service.calls == ["GetServerInfo", "SyncAttributeIndexes"]
        worker.wait_pull_registered(timeout=5)
        assert not service.polling_during_sync
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
            pull=PullWorkerOptions(max_concurrent_tasks=2),
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
                minimum_supported_protocol_version=8,
                current_protocol_version=8,
            ),
            None,
            r"supports Dex Server protocol versions 4-7.*"
            r"protocol versions 8-8.*Upgrade the SDK",
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
            pull=PullWorkerOptions(max_concurrent_tasks=2),
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


@pytest.mark.parametrize("is_async", [False, True])
def test_worker_rejects_protocol_3_before_polling(is_async: bool) -> None:
    asyncio.run(_worker_rejects_protocol_3_before_polling(is_async))


async def _worker_rejects_protocol_3_before_polling(is_async: bool) -> None:
    service = SyncService(
        server_info=pb.ServerInfo(
            server_version="previous-3",
            minimum_supported_protocol_version=1,
            current_protocol_version=3,
        )
    )
    server, port = _start_flow_server(service)
    worker_type = AsyncWorker if is_async else Worker
    worker = worker_type(
        Registry([]),
        MemoryBlobCache(),
        WorkerOptions(
            pull=PullWorkerOptions(max_concurrent_tasks=2),
            server_address=f"127.0.0.1:{port}",
        ),
    )
    try:
        with pytest.raises(
            RuntimeError,
            match=r"requires a Dex Server with protocol version 4.*newer than v0.1.16.*protocol versions 1-3.*server version previous-3.*Upgrade the Server",
        ):
            if isinstance(worker, AsyncWorker):
                await worker.start()
            else:
                worker.start()
        assert service.calls == ["GetServerInfo"]
        assert service.poll_calls == []
    finally:
        if isinstance(worker, AsyncWorker):
            await worker.close()
        else:
            worker.close()
        server.stop(grace=None).wait(timeout=5)


def test_async_worker_synchronizes_indexes_before_polling() -> None:
    asyncio.run(_test_async_worker_synchronizes_indexes_before_polling())


async def _test_async_worker_synchronizes_indexes_before_polling() -> None:
    service = SyncService()
    flow_server, flow_port = _start_flow_server(service)
    worker = AsyncWorker(
        Registry((IndexedFlow(),), allow_async_handlers=True),
        MemoryBlobCache(),
        WorkerOptions(
            pull=PullWorkerOptions(max_concurrent_tasks=2),
            server_address=f"127.0.0.1:{flow_port}",
        ),
    )
    worker_task = asyncio.create_task(worker.start())
    try:
        called = await asyncio.to_thread(service.called.wait, 5)
        assert called
        await worker.wait_pull_registered(timeout=5)
        assert not service.polling_during_sync
    finally:
        await worker.stop()
        await asyncio.wait_for(worker_task, timeout=5)
        flow_server.stop(grace=None).wait(timeout=5)


class StartupResponseGate:
    def __init__(
        self, call: Callable[..., Awaitable[Any]], should_fail: bool = False
    ) -> None:
        self.call = call
        self.should_fail = should_fail
        self.received = asyncio.Event()
        self.release = asyncio.Event()

    async def __call__(self, *args: Any, **kwargs: Any) -> Any:
        response = await self.call(*args, **kwargs)
        self.received.set()
        await self.release.wait()
        if self.should_fail:
            raise RuntimeError("startup RPC failed")
        return response


@pytest.mark.parametrize("rpc_name", ["GetServerInfo", "SyncAttributeIndexes"])
@pytest.mark.parametrize("shutdown", ["stop", "close"])
@pytest.mark.parametrize("should_fail", [False, True])
def test_async_worker_shutdown_during_start(
    rpc_name: str, shutdown: str, should_fail: bool, monkeypatch: pytest.MonkeyPatch
) -> None:
    asyncio.run(
        _async_worker_shutdown_during_start(
            rpc_name, shutdown, should_fail, monkeypatch
        )
    )


async def _async_worker_shutdown_during_start(
    rpc_name: str, shutdown: str, should_fail: bool, monkeypatch: pytest.MonkeyPatch
) -> None:
    service = SyncService()
    server, port = _start_flow_server(service)
    worker = AsyncWorker(
        Registry((IndexedFlow(),), allow_async_handlers=True),
        MemoryBlobCache(),
        WorkerOptions(
            pull=PullWorkerOptions(max_concurrent_tasks=2),
            server_address=f"127.0.0.1:{port}",
        ),
    )
    gate = StartupResponseGate(getattr(worker._flow_service, rpc_name), should_fail)
    monkeypatch.setattr(worker._flow_service, rpc_name, gate)
    worker_task = asyncio.create_task(worker.start())
    readiness = asyncio.create_task(worker.wait_pull_registered(timeout=5))
    try:
        await asyncio.wait_for(gate.received.wait(), timeout=5)
        await asyncio.wait_for(getattr(worker, shutdown)(), timeout=5)
        with pytest.raises(RuntimeError, match="stopped"):
            await readiness
        gate.release.set()
        await asyncio.wait_for(worker_task, timeout=2)
        assert worker._state == ("closed" if shutdown == "close" else "stopped")
        assert service.poll_calls == []
    finally:
        gate.release.set()
        await worker.close()
        if not worker_task.done():
            worker_task.cancel()
        await asyncio.gather(worker_task, readiness, return_exceptions=True)
        server.stop(grace=None).wait(timeout=5)


def test_async_worker_explicit_cancel_after_close(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    asyncio.run(_async_worker_explicit_cancel_after_close(monkeypatch))


async def _async_worker_explicit_cancel_after_close(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    service = SyncService()
    server, port = _start_flow_server(service)
    worker = AsyncWorker(
        Registry([], allow_async_handlers=True),
        MemoryBlobCache(),
        WorkerOptions(server_address=f"127.0.0.1:{port}"),
    )
    gate = StartupResponseGate(worker._flow_service.GetServerInfo)
    monkeypatch.setattr(worker._flow_service, "GetServerInfo", gate)
    worker_task = asyncio.create_task(worker.start())
    try:
        await asyncio.wait_for(gate.received.wait(), timeout=5)
        await worker.close()
        worker_task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await asyncio.wait_for(worker_task, timeout=5)
        assert worker._state == "closed"
        assert service.poll_calls == []
    finally:
        gate.release.set()
        await worker.close()
        if not worker_task.done():
            worker_task.cancel()
        await asyncio.gather(worker_task, return_exceptions=True)
        server.stop(grace=None).wait(timeout=5)


@asynccontextmanager
async def _worker_with_pending_startup_rpc(
    rpc_name: str,
) -> AsyncIterator[tuple[AsyncWorker, asyncio.Task[None], SyncService]]:
    service = SyncService(delayed_rpc=rpc_name)
    server, port = _start_flow_server(service)
    worker = AsyncWorker(
        Registry((IndexedFlow(),), allow_async_handlers=True),
        MemoryBlobCache(),
        WorkerOptions(
            pull=PullWorkerOptions(max_concurrent_tasks=2),
            server_address=f"127.0.0.1:{port}",
        ),
    )
    worker_task = asyncio.create_task(worker.start())
    try:
        assert await asyncio.to_thread(service.rpc_started.wait, 5)
        yield worker, worker_task, service
    finally:
        service.release_rpc.set()
        await worker.close()
        if not worker_task.done():
            worker_task.cancel()
        await asyncio.gather(worker_task, return_exceptions=True)
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize("rpc_name", ["GetServerInfo", "SyncAttributeIndexes"])
@pytest.mark.parametrize("shutdown", ["stop", "close"])
def test_async_worker_shutdown_cancels_pending_start_rpc(
    rpc_name: str, shutdown: str
) -> None:
    asyncio.run(_async_worker_shutdown_cancels_pending_start_rpc(rpc_name, shutdown))


async def _async_worker_shutdown_cancels_pending_start_rpc(
    rpc_name: str, shutdown: str
) -> None:
    async with _worker_with_pending_startup_rpc(rpc_name) as (
        worker,
        worker_task,
        service,
    ):
        await asyncio.wait_for(getattr(worker, shutdown)(), timeout=5)
        await asyncio.wait_for(worker_task, timeout=5)
        with pytest.raises(RuntimeError, match="stopped"):
            await worker.wait_pull_registered(timeout=5)
        assert worker._state == ("closed" if shutdown == "close" else "stopped")
        assert worker._flow_channel.get_state() == grpc.ChannelConnectivity.SHUTDOWN
        assert service.poll_calls == []


@pytest.mark.parametrize("rpc_name", ["GetServerInfo", "SyncAttributeIndexes"])
def test_async_worker_cancel_during_start_releases_resources(rpc_name: str) -> None:
    asyncio.run(_async_worker_cancel_during_start_releases_resources(rpc_name))


async def _async_worker_cancel_during_start_releases_resources(rpc_name: str) -> None:
    async with _worker_with_pending_startup_rpc(rpc_name) as (
        worker,
        worker_task,
        service,
    ):
        worker_task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await asyncio.wait_for(worker_task, timeout=5)
        assert worker._state == "stopped"
        assert worker._flow_channel.get_state() == grpc.ChannelConnectivity.SHUTDOWN
        assert service.poll_calls == []


def test_async_worker_concurrent_close_during_start() -> None:
    asyncio.run(_async_worker_concurrent_close_during_start())


async def _async_worker_concurrent_close_during_start() -> None:
    async with _worker_with_pending_startup_rpc("GetServerInfo") as (
        worker,
        worker_task,
        service,
    ):
        await asyncio.wait_for(
            asyncio.gather(worker.close(), worker.stop(), worker.close()), timeout=5
        )
        await asyncio.wait_for(worker_task, timeout=5)
        with pytest.raises(RuntimeError, match="stopped"):
            await worker.wait_pull_registered(timeout=5)
        assert worker._state == "closed"
        assert worker._flow_channel.get_state() == grpc.ChannelConnectivity.SHUTDOWN
        assert service.poll_calls == []


@pytest.mark.parametrize("is_async", [False, True])
@pytest.mark.parametrize(
    "code", [grpc.StatusCode.PERMISSION_DENIED, grpc.StatusCode.INVALID_ARGUMENT]
)
def test_registration_waiter_settles_when_every_stream_is_rejected(
    is_async: bool, code: grpc.StatusCode
) -> None:
    asyncio.run(
        _registration_waiter_settles_when_every_stream_is_rejected(is_async, code)
    )


async def _registration_waiter_settles_when_every_stream_is_rejected(
    is_async: bool, code: grpc.StatusCode
) -> None:
    service = SyncService(poll_rejection=code)
    flow_server, flow_port = _start_flow_server(service)
    options = WorkerOptions(
        pull=PullWorkerOptions(max_concurrent_tasks=2),
        server_address=f"127.0.0.1:{flow_port}",
    )
    try:
        if is_async:
            async_worker = AsyncWorker(
                Registry((IndexedFlow(),), allow_async_handlers=True),
                MemoryBlobCache(),
                options,
            )
            worker_task = asyncio.create_task(async_worker.start())
            try:
                with pytest.raises(PullRegistrationError) as rejected:
                    await async_worker.wait_pull_registered(timeout=5)
                assert not async_worker.pull_registration_state().is_ready
            finally:
                await async_worker.stop()
                await asyncio.wait_for(worker_task, timeout=5)
        else:
            worker = Worker(Registry((IndexedFlow(),)), MemoryBlobCache(), options)
            thread = threading.Thread(target=worker.start)
            thread.start()
            try:
                with pytest.raises(PullRegistrationError) as rejected:
                    await asyncio.to_thread(worker.wait_pull_registered, 5)
                assert not worker.pull_registration_state().is_ready
            finally:
                await asyncio.to_thread(worker.stop)
                thread.join(timeout=5)
            assert not thread.is_alive()
        assert cast(grpc.RpcError, rejected.value.error).code() == code
    finally:
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
