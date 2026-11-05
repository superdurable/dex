# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import logging
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta
from typing import Any, Callable, cast
from unittest.mock import AsyncMock, patch

import grpc
import pytest

from dex import PullWorkerOptions, Registry, _pull_worker
from dex._pull_worker import PullStream, PullWorkerRuntime
from dex.dexpb import dex_pb2 as pb
from test_pull_worker import (
    BlockingDispatcher,
    DelayedEofCall,
    FakeCall,
    FakeRpcError,
    FakeStub,
    QueueCall,
    RegistrationCall,
    _poll_until_closed,
)
from test_reserved_slot_dispatch import RegistrationLimitStub

_EOF = getattr(grpc.aio, "EOF")


@pytest.fixture(autouse=True)
def accept_fake_server_tasks(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        PullWorkerRuntime, "unknown_task_reason", lambda self, task: None
    )


async def wait_until(predicate: Callable[[], bool]) -> None:
    async with asyncio.timeout(2):
        while not predicate():
            await asyncio.sleep(0)


def rpc_task(task_id: str) -> pb.PollWorkerTaskResponse:
    return pb.PollWorkerTaskResponse(
        task=pb.PollWorkerTask(task_id=task_id, rpc=pb.InvokeWorkerRPCRequest())
    )


def registered(limit: int = 0) -> pb.PollWorkerTaskResponse:
    return pb.PollWorkerTaskResponse(
        registered=pb.PollWorkerTaskRegistered(
            max_concurrent_tasks_per_app_worker=limit
        )
    )


def is_reset_after_half_close(operations: list[str]) -> bool:
    if "cancel" not in operations:
        return True
    return "done_writing" in operations and operations.index(
        "done_writing"
    ) < operations.index("cancel")


class CountingDispatcher:
    def __init__(self) -> None:
        self.task_count = 0

    async def invoke_rpc(
        self, request: pb.InvokeWorkerRPCRequest, is_active: object
    ) -> pb.InvokeWorkerRPCResponse:
        self.task_count += 1
        return pb.InvokeWorkerRPCResponse()


class SyncCountingDispatcher:
    def __init__(self) -> None:
        self.task_count = 0

    def invoke_rpc(
        self, request: pb.InvokeWorkerRPCRequest, is_active: object
    ) -> pb.InvokeWorkerRPCResponse:
        self.task_count += 1
        return pb.InvokeWorkerRPCResponse()


class SlotReuseCall(QueueCall):
    """Fake Server that reuses the slot as soon as it receives a final output."""

    def __init__(self, total_task_count: int) -> None:
        super().__init__([registered(), rpc_task("task-1")])
        self.total_task_count = total_task_count
        self.assigned_task_count = 1

    async def write(self, request: pb.PollWorkerTaskRequest) -> None:
        await super().write(request)
        if not request.HasField("task_frame"):
            return
        if self.assigned_task_count < self.total_task_count:
            self.assigned_task_count += 1
            self.frames.put_nowait(rpc_task(f"task-{self.assigned_task_count}"))
        else:
            self.frames.put_nowait(_EOF)
        # Local write completion lags the Server's receipt of the output.
        for _ in range(3):
            await asyncio.sleep(0)


@pytest.mark.parametrize("is_sync", [False, True])
def test_next_task_right_after_final_output_reuses_slot(is_sync: bool) -> None:
    asyncio.run(_next_task_right_after_final_output_reuses_slot(is_sync))


async def _next_task_right_after_final_output_reuses_slot(is_sync: bool) -> None:
    total_task_count = 50
    call = SlotReuseCall(total_task_count)
    dispatcher: CountingDispatcher | SyncCountingDispatcher = (
        SyncCountingDispatcher() if is_sync else CountingDispatcher()
    )
    with ThreadPoolExecutor(max_workers=1) as executor:
        runtime = PullWorkerRuntime(
            Registry([]),
            dispatcher,
            PullWorkerOptions(max_concurrent_tasks=1),
            "unused",
            executor if is_sync else None,
        )
        runtime._loop = asyncio.get_running_loop()
        runtime._stopping = asyncio.Event()
        stream = PullStream(
            runtime,
            FakeStub(cast(FakeCall, call)),
            pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
        )
        assert await asyncio.wait_for(stream._poll(), 5)
    final_outputs = [
        request.task_frame.task_id
        for request in call.requests
        if request.HasField("task_frame")
    ]
    assert dispatcher.task_count == total_task_count
    assert final_outputs == [f"task-{n}" for n in range(1, total_task_count + 1)]
    assert not any(request.HasField("reject") for request in call.requests)


class HeldFinalWriteCall(DelayedEofCall):
    def __init__(
        self, frames: list[pb.PollWorkerTaskResponse], next_task_id: str | None
    ) -> None:
        super().__init__(frames)
        self.next_task_id = next_task_id
        self.final_write_started = asyncio.Event()
        self.final_write_allowed = asyncio.Event()

    async def write(self, request: pb.PollWorkerTaskRequest) -> None:
        if request.HasField("task_frame"):
            if self.next_task_id is not None:
                # The Server already has the output and reuses the slot.
                self.frames.put_nowait(rpc_task(self.next_task_id))
            self.final_write_started.set()
            await self.final_write_allowed.wait()
            if self.is_closed:
                raise FakeRpcError(grpc.StatusCode.CANCELLED)
        await super().write(request)

    def cancel(self) -> None:
        super().cancel()
        self.final_write_allowed.set()


def test_stop_racing_next_task_after_final_output_drops_it_and_half_closes() -> None:
    asyncio.run(_stop_racing_next_task_after_final_output_drops_it_and_half_closes())


async def _stop_racing_next_task_after_final_output_drops_it_and_half_closes() -> None:
    call = HeldFinalWriteCall([registered(), rpc_task("first")], "next")
    dispatcher = CountingDispatcher()
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
    with patch.object(stream, "_start_task", wraps=stream._start_task) as start_task:
        stream.run_task = asyncio.create_task(_poll_until_closed(stream))
        runtime._streams.add(stream)
        await asyncio.wait_for(call.final_write_started.wait(), 2)
        # The next task waits for the previous handler, which is still writing.
        await wait_until(
            lambda: any(
                invocation.args[0].task_id == "next"
                for invocation in start_task.call_args_list
            )
        )
        stopping = asyncio.create_task(runtime.stop())
        await runtime._stopping.wait()
        call.final_write_allowed.set()
        await asyncio.wait_for(call.half_closed.wait(), 2)
        call.release_eof()
        await asyncio.wait_for(stopping, 2)
    assert dispatcher.task_count == 1
    assert [
        request.task_frame.task_id
        for request in call.requests
        if request.HasField("task_frame")
    ] == ["first"]
    assert not any(request.HasField("reject") for request in call.requests)
    assert call.operations.index("write:task_frame") < call.operations.index(
        "done_writing"
    )
    assert is_reset_after_half_close(call.operations)


@pytest.mark.parametrize("max_concurrent_tasks", [2, 4])
def test_registration_limit_failure_never_reports_ready(
    max_concurrent_tasks: int,
) -> None:
    asyncio.run(_registration_limit_failure_never_reports_ready(max_concurrent_tasks))


async def _registration_limit_failure_never_reports_ready(
    max_concurrent_tasks: int,
) -> None:
    runtime = PullWorkerRuntime(
        Registry([]),
        None,
        PullWorkerOptions(max_concurrent_tasks=max_concurrent_tasks),
        "unused",
        None,
    )
    stub = RegistrationLimitStub()
    waiter = asyncio.create_task(runtime.registration.wait_async(2))
    with patch(
        "dex._pull_worker.outbound_worker_task_channel", return_value=AsyncMock()
    ), patch("dex._pull_worker.dex_pb2_grpc.WorkerTaskServiceStub", return_value=stub):
        with pytest.raises(RuntimeError, match="maxConcurrentTasksCap=1"):
            await asyncio.wait_for(runtime.run(), 2)
    with pytest.raises(RuntimeError, match="maxConcurrentTasksCap=1"):
        await waiter
    with pytest.raises(RuntimeError, match="maxConcurrentTasksCap=1"):
        runtime.registration.wait(0)
    snapshot = runtime.registration.snapshot()
    assert not snapshot.is_ready
    assert not any(queue.has_registered_stream for queue in snapshot.queues)
    assert stub.calls
    for call in stub.calls:
        assert call.is_half_closed
        assert not any(request.HasField("task_frame") for request in call.requests)


def test_registration_limit_failure_half_closes_and_drops_assigned_task() -> None:
    asyncio.run(_registration_limit_failure_half_closes_and_drops_assigned_task())


async def _registration_limit_failure_half_closes_and_drops_assigned_task() -> None:
    dispatcher = BlockingDispatcher()
    call = DelayedEofCall([registered(limit=1), rpc_task("after-limit")])
    runtime = PullWorkerRuntime(
        Registry([]),
        dispatcher,
        PullWorkerOptions(max_concurrent_tasks=2),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    stream.run_task = asyncio.create_task(_poll_until_closed(stream))
    runtime._streams.add(stream)
    await asyncio.wait_for(call.half_closed.wait(), 2)
    await wait_until(call.frames.empty)
    call.release_eof()
    await asyncio.wait_for(stream.run_task, 2)
    assert runtime._rejection_stop_task is not None
    await asyncio.wait_for(runtime._rejection_stop_task, 2)
    assert dispatcher.invocation_count == 0
    assert [request.WhichOneof("frame") for request in call.requests] == ["register"]
    assert is_reset_after_half_close(call.operations)
    assert not runtime.registration.snapshot().is_ready
    with pytest.raises(RuntimeError, match="maxConcurrentTasksCap=1"):
        await runtime.registration.wait_async(0)


def test_lower_limit_after_first_registration_only_warns(
    caplog: pytest.LogCaptureFixture,
) -> None:
    runtime = PullWorkerRuntime(
        Registry([]), None, PullWorkerOptions(max_concurrent_tasks=2), "unused", None
    )
    assert runtime.accept_registration(
        pb.PollWorkerTaskRegistered(max_concurrent_tasks_per_app_worker=2)
    )
    with caplog.at_level(logging.WARNING, logger="dex._pull_worker"):
        assert runtime.accept_registration(
            pb.PollWorkerTaskRegistered(max_concurrent_tasks_per_app_worker=1)
        )
    assert "maxConcurrentTasksCap" in caplog.text
    assert runtime._permanent_error is None
    assert runtime._rejection_stop_task is None


def test_registrations_after_limit_failure_are_not_recorded() -> None:
    asyncio.run(_registrations_after_limit_failure_are_not_recorded())


async def _registrations_after_limit_failure_are_not_recorded() -> None:
    runtime = PullWorkerRuntime(
        Registry([]), None, PullWorkerOptions(max_concurrent_tasks=2), "unused", None
    )
    runtime._stopping = asyncio.Event()
    assert not runtime.accept_registration(
        pb.PollWorkerTaskRegistered(max_concurrent_tasks_per_app_worker=1)
    )
    # A concurrent stream acknowledged with a sufficient limit still must not count.
    assert not runtime.accept_registration(
        pb.PollWorkerTaskRegistered(max_concurrent_tasks_per_app_worker=0)
    )
    assert runtime._rejection_stop_task is not None
    await asyncio.wait_for(runtime._rejection_stop_task, 2)
    assert not runtime.registration.snapshot().is_ready


class BlockedHalfCloseCall(DelayedEofCall):
    def __init__(self, frames: list[pb.PollWorkerTaskResponse]) -> None:
        super().__init__(frames)
        self.canceled = asyncio.Event()

    async def done_writing(self) -> None:
        await super().done_writing()
        await self.canceled.wait()

    def cancel(self) -> None:
        super().cancel()
        self.canceled.set()


@pytest.mark.parametrize("is_half_close_blocked", [False, True])
def test_stop_resets_streams_still_open_at_the_deadline(
    is_half_close_blocked: bool,
) -> None:
    asyncio.run(_stop_resets_streams_still_open_at_the_deadline(is_half_close_blocked))


async def _stop_resets_streams_still_open_at_the_deadline(
    is_half_close_blocked: bool,
) -> None:
    call = (
        BlockedHalfCloseCall([registered()])
        if is_half_close_blocked
        else DelayedEofCall([registered()])
    )
    runtime = PullWorkerRuntime(
        Registry([]),
        None,
        PullWorkerOptions(
            max_concurrent_tasks=1, stop_drain_timeout=timedelta(milliseconds=50)
        ),
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
    loop = asyncio.get_running_loop()
    started_at = loop.time()
    # The Server never sends EOF, as in a network partition.
    await asyncio.wait_for(runtime.stop(), 2)
    assert loop.time() - started_at < 1
    assert call.is_half_closed
    assert call.is_closed
    assert call.operations.index("done_writing") < call.operations.index("cancel")


@pytest.mark.parametrize("is_busy", [False, True])
@pytest.mark.parametrize(
    "failure",
    ["undecodable", "oversized", "local_exception", "unexpected_eof"],
)
def test_local_stream_error_resets_instead_of_half_closing(
    is_busy: bool, failure: str
) -> None:
    asyncio.run(_local_stream_error_resets_instead_of_half_closing(is_busy, failure))


async def _local_stream_error_resets_instead_of_half_closing(
    is_busy: bool, failure: str
) -> None:
    dispatcher = BlockingDispatcher()
    frames = [registered()]
    if is_busy:
        frames.append(rpc_task("running"))
    call = RegistrationCall(frames)
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
    polling = asyncio.create_task(stream._poll())
    if is_busy:
        await dispatcher.started.wait()
    else:
        await runtime.registration.wait_async(2)
    injected: Any = {
        "undecodable": FakeRpcError(grpc.StatusCode.INTERNAL),
        "oversized": FakeRpcError(grpc.StatusCode.RESOURCE_EXHAUSTED),
        "local_exception": ValueError("malformed task frame"),
        "unexpected_eof": _EOF,
    }[failure]
    call.frames.put_nowait(injected)
    if failure == "unexpected_eof":
        await asyncio.wait_for(polling, 2)
    else:
        with pytest.raises((grpc.RpcError, ValueError)):
            await asyncio.wait_for(polling, 2)
    assert call.is_closed
    assert not call.is_half_closed
    assert not any(request.HasField("task_frame") for request in call.requests)
    if is_busy:
        assert dispatcher.canceled.is_set()


class FailingFinalWriteCall(RegistrationCall):
    async def write(self, request: pb.PollWorkerTaskRequest) -> None:
        if request.HasField("task_frame"):
            raise FakeRpcError(grpc.StatusCode.INTERNAL)
        await super().write(request)


def test_failed_final_output_write_resets_draining_stream() -> None:
    asyncio.run(_failed_final_output_write_resets_draining_stream())


async def _failed_final_output_write_resets_draining_stream() -> None:
    dispatcher = BlockingDispatcher()
    call = FailingFinalWriteCall([registered(), rpc_task("unsent")])
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
    polling = asyncio.create_task(stream._poll())
    await dispatcher.started.wait()
    call.frames.put_nowait(
        pb.PollWorkerTaskResponse(
            reconnect=pb.PollWorkerTaskReconnect(reason="rebalance")
        )
    )
    await wait_until(lambda: stream.is_draining)
    dispatcher.gate.set()
    with pytest.raises(RuntimeError, match="without sending its final output"):
        await asyncio.wait_for(polling, 2)
    assert call.is_closed
    assert not call.is_half_closed


@pytest.mark.parametrize("is_flushed_within_grace", [True, False])
def test_stop_deadline_flushes_final_output_already_in_flight(
    is_flushed_within_grace: bool, monkeypatch: pytest.MonkeyPatch
) -> None:
    if not is_flushed_within_grace:
        monkeypatch.setattr(_pull_worker, "_FINAL_OUTPUT_FLUSH_GRACE_SECONDS", 0.3)
    asyncio.run(
        _stop_deadline_flushes_final_output_already_in_flight(is_flushed_within_grace)
    )


async def _stop_deadline_flushes_final_output_already_in_flight(
    is_flushed_within_grace: bool,
) -> None:
    dispatcher = BlockingDispatcher()
    call = HeldFinalWriteCall([registered(), rpc_task("flushing")], None)
    idle_call = DelayedEofCall([registered()])
    runtime = PullWorkerRuntime(
        Registry([]),
        dispatcher,
        PullWorkerOptions(
            max_concurrent_tasks=2,
            reserved_rpc_tasks=0,
            stop_drain_timeout=timedelta(milliseconds=50),
        ),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    streams = [
        PullStream(
            runtime,
            FakeStub(cast(FakeCall, stream_call)),
            pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
        )
        for stream_call in (call, idle_call)
    ]
    for stream in streams:
        stream.run_task = asyncio.create_task(_poll_until_closed(stream))
        runtime._streams.add(stream)
    await dispatcher.started.wait()
    await wait_until(lambda: bool(idle_call.requests))
    dispatcher.gate.set()
    await asyncio.wait_for(call.final_write_started.wait(), 2)
    stopping = asyncio.create_task(runtime.stop())
    await asyncio.sleep(0.15)
    # The idle stream has no Server EOF, so the drain deadline resets it.
    assert idle_call.is_closed
    assert idle_call.operations.index("done_writing") < idle_call.operations.index(
        "cancel"
    )
    assert not call.is_closed
    assert not stopping.done()
    if not is_flushed_within_grace:
        await asyncio.wait_for(stopping, 2)
        assert call.is_closed
        assert "done_writing" not in call.operations
        return
    call.final_write_allowed.set()
    await asyncio.wait_for(call.half_closed.wait(), 2)
    call.release_eof()
    await asyncio.wait_for(stopping, 2)
    assert not dispatcher.canceled.is_set()
    assert [
        request.task_frame.task_id
        for request in call.requests
        if request.HasField("task_frame")
    ] == ["flushing"]
    assert call.operations.index("write:task_frame") < call.operations.index(
        "done_writing"
    )
    assert is_reset_after_half_close(call.operations)


class UnconfirmedOutputCall(DelayedEofCall):
    """Fake Server that has not yet read the final output or ended the stream."""

    def __init__(
        self, frames: list[pb.PollWorkerTaskResponse], is_half_close_blocked: bool
    ) -> None:
        super().__init__(frames)
        self.half_close_allowed = asyncio.Event()
        if not is_half_close_blocked:
            self.half_close_allowed.set()

    async def done_writing(self) -> None:
        await super().done_writing()
        await self.half_close_allowed.wait()

    def cancel(self) -> None:
        super().cancel()
        self.half_close_allowed.set()


@pytest.mark.parametrize("is_released_within_grace", [True, False])
@pytest.mark.parametrize("is_half_close_blocked", [False, True])
@pytest.mark.parametrize("is_half_closed_before_stop", [False, True])
def test_final_output_sent_before_deadline_keeps_flush_grace(
    is_released_within_grace: bool,
    is_half_close_blocked: bool,
    is_half_closed_before_stop: bool,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(_pull_worker, "_FINAL_OUTPUT_FLUSH_GRACE_SECONDS", 0.6)
    asyncio.run(
        _final_output_sent_before_deadline_keeps_flush_grace(
            is_released_within_grace,
            is_half_close_blocked,
            is_half_closed_before_stop,
        )
    )


async def _final_output_sent_before_deadline_keeps_flush_grace(
    is_released_within_grace: bool,
    is_half_close_blocked: bool,
    is_half_closed_before_stop: bool,
) -> None:
    dispatcher = BlockingDispatcher()
    call = UnconfirmedOutputCall(
        [registered(), rpc_task("sent-before-deadline")], is_half_close_blocked
    )
    runtime = PullWorkerRuntime(
        Registry([]),
        dispatcher,
        PullWorkerOptions(
            max_concurrent_tasks=1, stop_drain_timeout=timedelta(milliseconds=100)
        ),
        "unused",
        None,
    )
    runtime._stopping = asyncio.Event()
    stream = PullStream(
        runtime, FakeStub(cast(FakeCall, call)), pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
    )
    stream.run_task = asyncio.create_task(_poll_until_closed(stream))
    runtime._streams.add(stream)
    await dispatcher.started.wait()
    loop = asyncio.get_running_loop()
    if is_half_closed_before_stop:
        call.frames.put_nowait(
            pb.PollWorkerTaskResponse(
                reconnect=pb.PollWorkerTaskReconnect(reason="rebalance")
            )
        )
        await wait_until(lambda: stream.is_draining)
        dispatcher.gate.set()
        await asyncio.wait_for(call.half_closed.wait(), 2)
        started_at = loop.time()
        stopping = asyncio.create_task(runtime.stop())
    else:
        started_at = loop.time()
        stopping = asyncio.create_task(runtime.stop())
        await runtime._stopping.wait()
        dispatcher.gate.set()
        await asyncio.wait_for(call.half_closed.wait(), 2)
    assert stream.handler_task is None
    # The handler finished and its output was written, but the Server has not read it.
    await asyncio.sleep(0.3)
    assert not call.is_closed
    assert not stopping.done()
    if is_released_within_grace:
        call.half_close_allowed.set()
        call.release_eof()
        await asyncio.wait_for(stopping, 2)
        operations = call.operations
        assert operations.index("write:task_frame") < operations.index("done_writing")
        assert operations.index("done_writing") < operations.index("read:EOF")
        if "cancel" in operations:
            assert operations.index("read:EOF") < operations.index("cancel")
    else:
        await asyncio.wait_for(stopping, 2)
        assert loop.time() - started_at >= 0.6
        assert call.is_closed
        # The fake call reports EOF after its own reset.
        operations = call.operations
        assert "read:EOF" not in operations or operations.index(
            "cancel"
        ) < operations.index("read:EOF")
    assert [
        request.task_frame.task_id
        for request in call.requests
        if request.HasField("task_frame")
    ] == ["sent-before-deadline"]
    assert not dispatcher.canceled.is_set()


def malformed_task(problem: str) -> pb.PollWorkerTask:
    task = pb.PollWorkerTask(task_id="malformed", rpc=pb.InvokeWorkerRPCRequest())
    if problem == "deadline_overflow":
        task.deadline.seconds = 2**62
    elif problem == "deadline_nanos":
        task.deadline.nanos = -1
    else:
        task.ClearField("rpc")
    return task


@pytest.mark.parametrize("is_sync", [False, True])
@pytest.mark.parametrize(
    "problem", ["deadline_overflow", "deadline_nanos", "missing_request"]
)
def test_malformed_task_resets_stream_without_admission(
    is_sync: bool, problem: str
) -> None:
    asyncio.run(_malformed_task_resets_stream_without_admission(is_sync, problem))


async def _malformed_task_resets_stream_without_admission(
    is_sync: bool, problem: str
) -> None:
    call = DelayedEofCall(
        [
            registered(),
            pb.PollWorkerTaskResponse(task=malformed_task(problem)),
        ]
    )
    dispatcher: CountingDispatcher | SyncCountingDispatcher = (
        SyncCountingDispatcher() if is_sync else CountingDispatcher()
    )
    with ThreadPoolExecutor(max_workers=1) as executor:
        runtime = PullWorkerRuntime(
            Registry([]),
            dispatcher,
            PullWorkerOptions(
                max_concurrent_tasks=1, stop_drain_timeout=timedelta(milliseconds=50)
            ),
            "unused",
            executor if is_sync else None,
        )
        runtime._loop = asyncio.get_running_loop()
        runtime._stopping = asyncio.Event()
        stream = PullStream(
            runtime,
            FakeStub(cast(FakeCall, call)),
            pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC,
        )
        with pytest.raises(RuntimeError, match="malformed"):
            await asyncio.wait_for(stream._poll(), 2)
        assert call.is_closed
        assert not call.is_half_closed
        assert "done_writing" not in call.operations
        assert not stream.is_busy
        assert stream.handler_task is None
        assert runtime._busy_tasks == 0
        await asyncio.wait_for(runtime.stop(), 1)
    assert dispatcher.task_count == 0
    assert not any(
        request.HasField("task_frame") or request.HasField("reject")
        for request in call.requests
    )


def test_poll_channel_accepts_only_identity_encoding() -> None:
    with patch("dex._pull_worker.grpc.aio.insecure_channel") as insecure_channel:
        _pull_worker.outbound_worker_task_channel("127.0.0.1:1")
    insecure_channel.assert_called_once_with(
        "127.0.0.1:1",
        options=(("grpc.compression_enabled_algorithms_bitset", 1),),
    )
