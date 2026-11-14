# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from typing import cast

import pytest

from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCache,
    Client,
    Flow,
    PullRegistrationError,
    PullWorkerOptions,
    StartFlowOptions,
    SubFlowOptions,
    Worker,
    WorkerOptions,
)
from dex._pull_worker import (
    PullStream,
    PullWorkerRuntime,
    TaskQueueRegistrationMismatchError,
)
from dex._server_protocol import NegotiatedServerProtocol
from dex._worker_dispatcher import WorkerDispatcher
from dex._value_hydrator import ValueHydrator
from dex._value_mapper import ValueMapper
from dex.dexpb import dex_pb2 as pb
from dex.flow import Registry
from test_hard_delete_attributes import ProtocolService
from test_hard_delete_attributes import _options as client_options
from test_hard_delete_attributes import _start_flow_server as start_client_server
from test_pull_worker import FakeCall, FakeStub, RegistrationCall
from test_worker_startup import MemoryBlobCache, SyncService
from test_worker_startup import _start_flow_server as start_worker_server


class QueuedFlow(Flow[None]):
    def get_flow_type(self) -> str:
        return "QueuedFlow"


INVALID_NAMES = ("x" * 64, "has space", "slash/queue", "é", "dex.other")


@pytest.mark.parametrize("name", INVALID_NAMES + ("dex.default",))
def test_worker_task_queue_rejects_invalid_and_reserved_names(name: str) -> None:
    with pytest.raises(ValueError, match="task_queue"):
        PullWorkerOptions(task_queue=name)


@pytest.mark.parametrize("name", INVALID_NAMES)
def test_flow_task_queue_rejects_invalid_names(name: str) -> None:
    with pytest.raises(ValueError, match="task_queue"):
        StartFlowOptions(task_queue=name)
    with pytest.raises(ValueError, match="task_queue"):
        SubFlowOptions(task_queue=name)


@pytest.mark.parametrize("name", ("", "gpu", "env-1.A_b", "q" * 63))
def test_task_queue_accepts_valid_names(name: str) -> None:
    assert PullWorkerOptions(task_queue=name).task_queue == name
    assert StartFlowOptions(task_queue=name).task_queue == name
    assert SubFlowOptions(task_queue=name).task_queue == name


def test_flow_task_queue_accepts_default_alias() -> None:
    assert StartFlowOptions(task_queue="dex.default").task_queue == "dex.default"
    assert SubFlowOptions(task_queue="dex.default").task_queue == "dex.default"


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_start_options_carry_task_queue(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    asyncio.run(_start_options_carry_task_queue(client_type))


async def _start_options_carry_task_queue(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    flow = QueuedFlow()
    registry = Registry([flow])
    client = client_type(registry, cast(BlobCache, object()))
    try:
        for task_queue in ("", "gpu", "dex.default"):
            mapped = client._map_start_options(
                registry._flow_for_instance(flow),
                StartFlowOptions(task_queue=task_queue),
            )
            assert mapped.flow_config_override.worker_target == pb.WorkerTarget(
                is_pull_mode=True, task_queue=task_queue
            )
    finally:
        if isinstance(client, AsyncClient):
            await client.close()
        else:
            client.close()


def test_sub_flow_options_require_protocol_5_for_task_queue() -> None:
    flow = QueuedFlow()
    registry = Registry([flow])
    protocol = NegotiatedServerProtocol()
    dispatcher = WorkerDispatcher(
        registry,
        ValueMapper(registry.codec_registry),
        cast(ValueHydrator, None),
        protocol,
    )
    target = registry._flow_for_instance(flow)
    protocol.record_version(4)
    assert dispatcher._map_sub_flow_options(
        target, SubFlowOptions()
    ).flow_config_override.worker_target == pb.WorkerTarget(is_pull_mode=True)
    with pytest.raises(RuntimeError, match="protocol version 5"):
        dispatcher._map_sub_flow_options(target, SubFlowOptions(task_queue="gpu"))
    protocol.record_version(5)
    for task_queue in ("gpu", "dex.default"):
        mapped = dispatcher._map_sub_flow_options(
            target, SubFlowOptions(task_queue=task_queue)
        )
        assert mapped.flow_config_override.worker_target == pb.WorkerTarget(
            is_pull_mode=True, task_queue=task_queue
        )


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
@pytest.mark.parametrize("task_queue", ["gpu", "dex.default"])
def test_client_refuses_task_queue_before_protocol_5(
    client_type: type[Client] | type[AsyncClient], task_queue: str
) -> None:
    asyncio.run(_client_refuses_task_queue_before_protocol_5(client_type, task_queue))


async def _client_refuses_task_queue_before_protocol_5(
    client_type: type[Client] | type[AsyncClient], task_queue: str
) -> None:
    service = ProtocolService(current_protocol_version=4)
    server, port = start_client_server(service)
    flow = QueuedFlow()
    client = client_type(
        Registry([flow]), cast(BlobCache, object()), client_options(port)
    )
    options = StartFlowOptions(task_queue=task_queue)
    try:
        with pytest.raises(RuntimeError, match="protocol version 5"):
            if isinstance(client, AsyncClient):
                await client.start_flow(flow, "queued", None, options)
            else:
                client.start_flow(flow, "queued", None, options)
        if isinstance(client, AsyncClient):
            await client.start_flow(flow, "default", None)
        else:
            client.start_flow(flow, "default", None)
        assert service.calls == ["GetServerInfo", "StartFlow:default"]
    finally:
        if isinstance(client, AsyncClient):
            await client.close()
        else:
            client.close()
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize("client_type", [Client, AsyncClient])
def test_client_sends_task_queue_to_protocol_5(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    asyncio.run(_client_sends_task_queue_to_protocol_5(client_type))


async def _client_sends_task_queue_to_protocol_5(
    client_type: type[Client] | type[AsyncClient],
) -> None:
    service = ProtocolService(current_protocol_version=5)
    server, port = start_client_server(service)
    flow = QueuedFlow()
    client = client_type(
        Registry([flow]), cast(BlobCache, object()), client_options(port)
    )
    options = StartFlowOptions(task_queue="gpu")
    try:
        if isinstance(client, AsyncClient):
            await client.start_flow(flow, "queued", None, options)
        else:
            client.start_flow(flow, "queued", None, options)
        assert service.calls == ["GetServerInfo", "StartFlow:queued"]
        target = service.start_requests[0].flow_start_options.flow_config_override
        assert target.worker_target.task_queue == "gpu"
    finally:
        if isinstance(client, AsyncClient):
            await client.close()
        else:
            client.close()
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize("is_async", [False, True])
def test_worker_refuses_task_queue_before_protocol_5(is_async: bool) -> None:
    asyncio.run(_worker_refuses_task_queue_before_protocol_5(is_async))


async def _worker_refuses_task_queue_before_protocol_5(is_async: bool) -> None:
    service = SyncService(
        server_info=pb.ServerInfo(
            server_version="protocol-4",
            minimum_supported_protocol_version=1,
            current_protocol_version=4,
        )
    )
    server, port = start_worker_server(service)
    worker_type = AsyncWorker if is_async else Worker
    worker = worker_type(
        Registry([QueuedFlow()]),
        MemoryBlobCache(),
        WorkerOptions(
            pull=PullWorkerOptions(max_concurrent_tasks=2, task_queue="gpu"),
            server_address=f"127.0.0.1:{port}",
        ),
    )
    try:
        with pytest.raises(RuntimeError, match="'gpu' requires Dex Server protocol"):
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


def _queued_runtime(task_queue: str) -> tuple[PullWorkerRuntime, asyncio.Event]:
    runtime = PullWorkerRuntime(
        Registry([QueuedFlow()]),
        None,
        PullWorkerOptions(
            max_concurrent_tasks=1,
            app_worker_id="queued-worker",
            task_queue=task_queue,
        ),
        "unused",
        None,
    )
    stopping = asyncio.Event()
    runtime._stopping = stopping
    return runtime, stopping


def test_registration_sends_task_queue_and_accepts_matching_echo() -> None:
    asyncio.run(_registration_sends_task_queue_and_accepts_matching_echo())


async def _registration_sends_task_queue_and_accepts_matching_echo() -> None:
    runtime, stopping = _queued_runtime("gpu")
    call = RegistrationCall(
        [
            pb.PollWorkerTaskResponse(
                registered=pb.PollWorkerTaskRegistered(task_queue="gpu")
            )
        ]
    )
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    stream.run_task = asyncio.create_task(stream.run())
    try:
        await runtime.registration.wait_async(1)
        register = call.requests[0].register
        assert register.task_queue == "gpu"
        assert register.flow_types == ["QueuedFlow"]
        state = runtime.registration.snapshot()
        assert state.is_ready
        assert [entry.task_queue for entry in state.queues] == ["gpu"]
    finally:
        stopping.set()
        call.cancel()
        await asyncio.wait_for(stream.run_task, 1)


@pytest.mark.parametrize(
    ("requested", "echoed"), [("gpu", ""), ("gpu", "cpu"), ("", "gpu")]
)
def test_registration_echo_mismatch_is_permanent_rejection(
    requested: str, echoed: str
) -> None:
    asyncio.run(_registration_echo_mismatch_is_permanent_rejection(requested, echoed))


async def _registration_echo_mismatch_is_permanent_rejection(
    requested: str, echoed: str
) -> None:
    runtime, stopping = _queued_runtime(requested)
    call = RegistrationCall(
        [
            pb.PollWorkerTaskResponse(
                registered=pb.PollWorkerTaskRegistered(task_queue=echoed)
            ),
            pb.PollWorkerTaskResponse(task=pb.PollWorkerTask(task_id="must-not-run")),
        ]
    )
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    stream.run_task = asyncio.create_task(stream.run())
    try:
        with pytest.raises(PullRegistrationError) as rejected:
            await runtime.registration.wait_async(1)
        assert rejected.value.queue == "STEP_AND_RPC"
        assert rejected.value.task_queue == requested
        assert isinstance(rejected.value.error, TaskQueueRegistrationMismatchError)
        assert rejected.value.error.registered_task_queue == echoed
        assert call.is_closed
        assert stream.task_id == ""
        state = runtime.registration.snapshot()
        assert not state.is_ready
        assert not state.queues[0].has_registered_stream
        assert state.queues[0].task_queue == requested
        assert state.queues[0].last_permanent_error is rejected.value.error
    finally:
        stopping.set()
        await asyncio.wait_for(stream.run_task, 1)


def test_echo_mismatch_stops_worker_when_configured() -> None:
    asyncio.run(_echo_mismatch_stops_worker_when_configured())


async def _echo_mismatch_stops_worker_when_configured() -> None:
    runtime = PullWorkerRuntime(
        Registry([QueuedFlow()]),
        None,
        PullWorkerOptions(
            max_concurrent_tasks=1,
            app_worker_id="queued-worker",
            task_queue="gpu",
            stop_on_permanent_stream_rejection=True,
        ),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    runtime._stop_completed = asyncio.Event()
    call = RegistrationCall(
        [pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered())]
    )
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    stream.run_task = asyncio.create_task(stream.run())
    await asyncio.wait_for(stream.run_task, 1)
    assert isinstance(runtime._permanent_error, PullRegistrationError)
    assert isinstance(
        runtime._permanent_error.error, TaskQueueRegistrationMismatchError
    )
    if runtime._rejection_stop_task is not None:
        await asyncio.wait_for(runtime._rejection_stop_task, 1)
