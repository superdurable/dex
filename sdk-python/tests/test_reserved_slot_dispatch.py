# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta
from typing import Any, Callable, cast
from unittest.mock import AsyncMock, patch

import grpc
import pytest

from dex import (
    AsyncClient,
    Attribute,
    BlobCache,
    Client,
    ClientOptions,
    Flow,
    PullWorkerOptions,
    RecoveryErrorInfo,
    Registry,
    StepAttemptProgress,
)
from dex._invocation_context import InvocationContext, InvocationMethod
from dex._pull_worker import PullStream, PullWorkerRuntime
from dex._value_mapper import ValueMapper
from dex.dexpb import dex_pb2 as pb
from test_hard_delete_attributes import ProtocolService, SecretFlow, _start_flow_server
from test_pull_worker import (
    BlockingDispatcher,
    BurstStub,
    DelayedEofCall,
    FakeCall,
    FakeRpcError,
    FakeStub,
    RegistrationCall,
    SyncCancelDispatcher,
    _poll_until_closed,
)


@pytest.fixture(autouse=True)
def accept_fake_server_tasks(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        PullWorkerRuntime, "unknown_task_reason", lambda self, task: None
    )


async def wait_until(predicate: Callable[[], bool]) -> None:
    async with asyncio.timeout(2):
        while not predicate():
            await asyncio.sleep(0)


@pytest.mark.parametrize("reserved_count", [0, 1, 2, None])
def test_registration_fields_and_fixed_stream_count(reserved_count: int | None) -> None:
    asyncio.run(_registration_fields_and_fixed_stream_count(reserved_count))


async def _registration_fields_and_fixed_stream_count(
    reserved_count: int | None,
) -> None:
    runtime = PullWorkerRuntime(
        Registry([Flow[None]()]),
        None,
        PullWorkerOptions(
            max_concurrent_tasks=4,
            reserved_rpc_tasks=reserved_count,
            idle_streams_per_queue=17,
            app_worker_id="slots",
        ),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    stub = BurstStub()
    runtime._reconcile(stub)
    try:
        await runtime.registration.wait_async(2)
        assert len(runtime._streams) == len(stub.calls) == 4
        registers = [call.requests[0].register for call in stub.calls]
        assert sum(
            register.slot_kind == pb.POLL_WORKER_SLOT_KIND_RPC_ONLY
            for register in registers
        ) == (1 if reserved_count is None else reserved_count)
        for register in registers:
            assert register.queue == ""
            assert register.app_worker_id == "slots"
            assert list(register.flow_types) == ["Flow"]
            assert register.supports_reserved_slot_dispatch
        for stream in runtime._streams:
            stream.is_busy = True
        runtime._reconcile(stub)
        assert len(runtime._streams) == 4
        for stream in runtime._streams:
            stream.is_busy = False
        assert not any(
            request.HasField("reject")
            for call in stub.calls
            for request in call.requests
        )
    finally:
        await runtime.stop()


@pytest.mark.parametrize("reported_limit", [0, 2, 3])
def test_registration_limit_allows_sufficient_or_unreported_limit(
    reported_limit: int,
) -> None:
    runtime = PullWorkerRuntime(
        Registry([]), None, PullWorkerOptions(max_concurrent_tasks=2), "unused", None
    )
    runtime.check_registration_limit(
        pb.PollWorkerTaskRegistered(max_concurrent_tasks_per_app_worker=reported_limit)
    )
    assert runtime._permanent_error is None


class RegistrationLimitStub:
    def __init__(self) -> None:
        self.calls: list[RegistrationCall] = []

    def PollWorkerTask(self) -> RegistrationCall:
        call = RegistrationCall(
            [
                pb.PollWorkerTaskResponse(
                    registered=pb.PollWorkerTaskRegistered(
                        max_concurrent_tasks_per_app_worker=1
                    )
                )
            ]
        )
        self.calls.append(call)
        return call


def test_start_fails_on_registration_limit_without_retry() -> None:
    asyncio.run(_start_fails_on_registration_limit_without_retry())


async def _start_fails_on_registration_limit_without_retry() -> None:
    runtime = PullWorkerRuntime(
        Registry([]), None, PullWorkerOptions(max_concurrent_tasks=2), "unused", None
    )
    stub = RegistrationLimitStub()
    with patch(
        "dex._pull_worker.outbound_worker_task_channel", return_value=AsyncMock()
    ), patch("dex._pull_worker.dex_pb2_grpc.WorkerTaskServiceStub", return_value=stub):
        with pytest.raises(
            RuntimeError,
            match=r"max_concurrent_tasks=2.*matching.maxConcurrentTasksCap=1",
        ):
            await asyncio.wait_for(runtime.run(), 2)
    assert len(stub.calls) == 2
    assert all(
        sum(request.HasField("register") for request in call.requests) == 1
        for call in stub.calls
    )


@pytest.mark.parametrize("slot_kind", ["STEP_AND_RPC", "RPC_ONLY"])
def test_slot_executes_assigned_step_without_capacity_rejection(slot_kind: str) -> None:
    asyncio.run(_slot_executes_assigned_step_without_capacity_rejection(slot_kind))


async def _slot_executes_assigned_step_without_capacity_rejection(
    slot_kind: str,
) -> None:
    runtime = PullWorkerRuntime(
        Registry([]), None, PullWorkerOptions(max_concurrent_tasks=2), "unused", None
    )
    runtime._stopping = asyncio.Event()
    stream = PullStream(
        runtime,
        None,
        cast(
            pb.PollWorkerSlotKind,
            pb.PollWorkerSlotKind.Value("POLL_WORKER_SLOT_KIND_" + slot_kind),
        ),
    )
    # The Server routes Steps away from RPC_ONLY; an erroneous assignment must still run.
    with patch.object(
        stream, "_execute", new_callable=AsyncMock
    ) as execute, patch.object(stream, "_send", new_callable=AsyncMock) as send:
        await stream._start_task(
            pb.PollWorkerTask(task_id="step", execute=pb.InvokeExecuteMethodRequest())
        )
        await stream._finish_handler()
        execute.assert_awaited_once()
        send.assert_not_awaited()
    assert runtime.reserved_rpc_declines == 0


@pytest.mark.parametrize("has_busy_task", [False, True])
def test_ta25_shutdown_drops_frames_and_half_closes_after_final_output(
    has_busy_task: bool,
) -> None:
    asyncio.run(
        _ta25_shutdown_drops_frames_and_half_closes_after_final_output(has_busy_task)
    )


async def _ta25_shutdown_drops_frames_and_half_closes_after_final_output(
    has_busy_task: bool,
) -> None:
    dispatcher = BlockingDispatcher()
    frames = [pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered())]
    if has_busy_task:
        frames.append(
            pb.PollWorkerTaskResponse(
                task=pb.PollWorkerTask(
                    task_id="before-stop", rpc=pb.InvokeWorkerRPCRequest()
                )
            )
        )
    call = DelayedEofCall(frames)
    runtime = PullWorkerRuntime(
        Registry([]),
        dispatcher,
        PullWorkerOptions(max_concurrent_tasks=1),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    stream.run_task = asyncio.create_task(_poll_until_closed(stream))
    runtime._streams.add(stream)
    await runtime.registration.wait_async(2)
    if has_busy_task:
        await dispatcher.started.wait()
    with patch.object(stream, "_start_task", wraps=stream._start_task) as start_task:
        stopping = asyncio.create_task(runtime.stop())
        await runtime._stopping.wait()
        call.frames.put_nowait(
            pb.PollWorkerTaskResponse(
                task=pb.PollWorkerTask(
                    task_id="after-stop-before-close", rpc=pb.InvokeWorkerRPCRequest()
                )
            )
        )
        if has_busy_task:
            await wait_until(call.frames.empty)
            assert not call.is_half_closed
            dispatcher.gate.set()
        await asyncio.wait_for(call.half_closed.wait(), 2)
        call.frames.put_nowait(
            pb.PollWorkerTaskResponse(
                task=pb.PollWorkerTask(
                    task_id="after-half-close", rpc=pb.InvokeWorkerRPCRequest()
                )
            )
        )
        await wait_until(call.frames.empty)
        assert not stopping.done()
        call.release_eof()
        await asyncio.wait_for(stopping, 2)
        # The read loop may pass a pre-close frame to the stop guard; no handler starts.
        assert all(
            invocation.args[0].task_id != "after-half-close"
            for invocation in start_task.call_args_list
        )
    outputs = [request for request in call.requests if request.HasField("task_frame")]
    assert len(outputs) == int(has_busy_task)
    assert not any(request.HasField("reject") for request in call.requests)
    if has_busy_task:
        assert call.operations.index("write:task_frame") < call.operations.index(
            "done_writing"
        )
    else:
        assert not dispatcher.started.is_set()
    assert not dispatcher.canceled.is_set()
    assert dispatcher.invocation_count == int(has_busy_task)


def test_ta25_sync_timeout_resets_stream_before_waiting_for_handler() -> None:
    asyncio.run(_ta25_sync_timeout_resets_stream_before_waiting_for_handler())


async def _ta25_sync_timeout_resets_stream_before_waiting_for_handler() -> None:
    dispatcher = SyncCancelDispatcher(should_return_result=True)
    call = DelayedEofCall(
        [
            pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered()),
            pb.PollWorkerTaskResponse(
                task=pb.PollWorkerTask(
                    task_id="slow-sync", rpc=pb.InvokeWorkerRPCRequest()
                )
            ),
        ]
    )
    with ThreadPoolExecutor(max_workers=1) as executor:
        runtime = PullWorkerRuntime(
            Registry([]),
            dispatcher,
            PullWorkerOptions(
                max_concurrent_tasks=1, stop_drain_timeout=timedelta(milliseconds=20)
            ),
            "unused",
            executor,
        )
        runtime._loop = asyncio.get_running_loop()
        runtime._stopping = asyncio.Event()
        stream = PullStream(
            runtime,
            FakeStub(cast(FakeCall, call)),
            pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
        )
        stream.run_task = asyncio.create_task(_poll_until_closed(stream))
        runtime._streams.add(stream)
        assert await asyncio.to_thread(dispatcher.started.wait, 2)
        await asyncio.wait_for(runtime.stop(), 2)
        assert call.is_closed
        assert not call.is_half_closed
        assert not any(request.HasField("task_frame") for request in call.requests)


def test_broken_stream_cancels_handler_and_never_replays_output() -> None:
    asyncio.run(_broken_stream_cancels_handler_and_never_replays_output())


async def _broken_stream_cancels_handler_and_never_replays_output() -> None:
    dispatcher = BlockingDispatcher()
    call = RegistrationCall(
        [
            pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered()),
            pb.PollWorkerTaskResponse(
                task=pb.PollWorkerTask(task_id="lost", rpc=pb.InvokeWorkerRPCRequest())
            ),
        ]
    )
    runtime = PullWorkerRuntime(
        Registry([]),
        dispatcher,
        PullWorkerOptions(max_concurrent_tasks=1),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    running = asyncio.create_task(stream._poll())
    await dispatcher.started.wait()
    call.frames.put_nowait(FakeRpcError(grpc.StatusCode.UNAVAILABLE))
    with pytest.raises(grpc.RpcError):
        await running
    assert dispatcher.canceled.is_set()
    assert not any(request.HasField("task_frame") for request in call.requests)
    replacement = RegistrationCall(
        [pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered())]
    )
    stream.stub = FakeStub(cast(FakeCall, replacement))
    running = asyncio.create_task(stream._poll())
    await wait_until(lambda: bool(replacement.requests))
    replacement.cancel()
    await running
    assert not any(request.HasField("task_frame") for request in replacement.requests)


class FinalWriteBlockingCall(RegistrationCall):
    def __init__(self) -> None:
        super().__init__(
            [
                pb.PollWorkerTaskResponse(registered=pb.PollWorkerTaskRegistered()),
                pb.PollWorkerTaskResponse(
                    task=pb.PollWorkerTask(
                        task_id="blocked-output", rpc=pb.InvokeWorkerRPCRequest()
                    )
                ),
            ]
        )
        self.output_pending = asyncio.Event()
        self.write_allowed = asyncio.Event()

    async def write(self, request: pb.PollWorkerTaskRequest) -> None:
        if request.HasField("task_frame"):
            self.output_pending.set()
            await self.write_allowed.wait()
            if self.is_closed:
                raise FakeRpcError(grpc.StatusCode.UNAVAILABLE)
        await super().write(request)

    def cancel(self) -> None:
        super().cancel()
        self.write_allowed.set()


def test_connection_break_cancels_blocked_final_write_without_deadlock() -> None:
    asyncio.run(_connection_break_cancels_blocked_final_write_without_deadlock())


async def _connection_break_cancels_blocked_final_write_without_deadlock() -> None:
    dispatcher = BlockingDispatcher()
    call = FinalWriteBlockingCall()
    runtime = PullWorkerRuntime(
        Registry([]),
        dispatcher,
        PullWorkerOptions(max_concurrent_tasks=1),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    running = asyncio.create_task(stream._poll())
    await dispatcher.started.wait()
    dispatcher.gate.set()
    await asyncio.wait_for(call.output_pending.wait(), 2)
    call.frames.put_nowait(FakeRpcError(grpc.StatusCode.UNAVAILABLE))
    with pytest.raises(grpc.RpcError):
        await asyncio.wait_for(running, 2)
    assert call.is_closed
    assert stream.handler_task is None
    assert not any(request.HasField("task_frame") for request in call.requests)


async def request_client_probe(client: Client | AsyncClient, operation: str) -> None:
    if isinstance(client, AsyncClient):
        if operation == "get_attributes":
            await client.get_attributes("probe", Attribute("plain", str))
        else:
            await client.health_check()
    elif operation == "get_attributes":
        client.get_attributes("probe", Attribute("plain", str))
    else:
        client.health_check()


@pytest.mark.parametrize("is_async", [False, True])
@pytest.mark.parametrize("should_fail_transiently", [False, True])
@pytest.mark.parametrize("operation", ["health_check", "get_attributes"])
def test_every_client_request_negotiates_lazily_and_caches_only_definitive_results(
    is_async: bool, should_fail_transiently: bool, operation: str
) -> None:
    asyncio.run(
        _every_client_request_negotiates_lazily_and_caches_only_definitive_results(
            is_async, should_fail_transiently, operation
        )
    )


async def _every_client_request_negotiates_lazily_and_caches_only_definitive_results(
    is_async: bool, should_fail_transiently: bool, operation: str
) -> None:
    service = ProtocolService(
        current_protocol_version=4 if should_fail_transiently else 3,
        server_info_failure=(
            grpc.StatusCode.UNAVAILABLE if should_fail_transiently else None
        ),
    )
    server, port = _start_flow_server(service)
    client_type = AsyncClient if is_async else Client
    client = client_type(
        Registry([SecretFlow()]),
        cast(BlobCache, object()),
        ClientOptions(server_address=f"127.0.0.1:{port}"),
    )
    assert service.calls == []
    try:
        first_error: RuntimeError | None = None
        for attempt_number in range(2):
            if should_fail_transiently and attempt_number:
                service.server_info_failure = None
                # Negotiation succeeds before a method this fake server does not implement.
                with pytest.raises(Exception) as method_error:
                    await request_client_probe(client, operation)
                assert "protocol" not in str(method_error.value)
            else:
                with pytest.raises(RuntimeError) as failure:
                    await request_client_probe(client, operation)
                if should_fail_transiently:
                    assert "GetServerInfo failed" in str(failure.value)
                else:
                    assert str(failure.value) == (
                        "This SDK requires a Dex Server with protocol version 4 or later "
                        "(server-zd newer than v0.1.16). The Server reports protocol versions "
                        "1-3 (server version test). Upgrade the Server before upgrading the SDK."
                    )
                    if first_error is not None:
                        assert failure.value is first_error
                    first_error = failure.value
        assert service.calls.count("GetServerInfo") == (
            2 if should_fail_transiently else 1
        )
    finally:
        if isinstance(client, AsyncClient):
            await client.close()
        else:
            client.close()
        server.stop(grace=None).wait(timeout=5)


@pytest.mark.parametrize(
    "protocol_progress,expected",
    [
        (0, StepAttemptProgress.UNSPECIFIED),
        (1, StepAttemptProgress.NOT_STARTED),
        (2, StepAttemptProgress.NOT_COMPLETED),
        (99, StepAttemptProgress.UNSPECIFIED),
    ],
)
def test_recovery_attempt_progress_mapping(
    protocol_progress: int, expected: StepAttemptProgress
) -> None:
    flow = Flow[None]()
    registry = Registry([flow])
    context = InvocationContext(
        InvocationMethod.EXECUTE,
        registry._flow_for_instance(flow),
        pb.Context(
            recovery_error=pb.RecoveryErrorInfo(
                detail="failure",
                error_type="dispatch",
                attempt_progress=cast(Any, protocol_progress),
            )
        ),
        ValueMapper(registry.codec_registry),
        (),
    )
    assert context.recovery_error == RecoveryErrorInfo("failure", "dispatch", expected)
    assert (
        RecoveryErrorInfo("legacy", "error").attempt_progress
        is StepAttemptProgress.UNSPECIFIED
    )
