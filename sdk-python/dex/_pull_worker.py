# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
import base64
import logging
import os
import random
import re
import socket
import threading
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
from typing import Any

import grpc

from dex._credential_transport import (
    AsyncCredentialRunner,
    _reject_credential_token,
    async_credential_metadata,
)
from dex._flow_service_tls import FlowServiceTransport
from dex._grpc_errors import _worker_error_status
from dex._message_size import grpc_message_size_channel_options
from dex._pull_slot_streams import (
    RPC_ONLY_QUEUE,
    STEP_AND_RPC_QUEUE,
    PullSlotStreams,
    _PullSlotAttempt,
    _SlotAttemptRevokedError,
)
from dex._server_protocol import sdk_version
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Registry
from dex.pull_registration import PullSlotStreamState, _PullRegistration
from dex.worker_options import PullWorkerOptions

_LOGGER = logging.getLogger(__name__)
_GRPC_AIO_EOF = getattr(grpc.aio, "EOF")
_BUSY_PULL_STREAM_KEEPALIVE_SECONDS = 20
# Extra stop time to flush a final output already being written at the deadline.
_FINAL_OUTPUT_FLUSH_GRACE_SECONDS = 5
_PERMANENT_CODES = {
    grpc.StatusCode.UNAUTHENTICATED,
    grpc.StatusCode.PERMISSION_DENIED,
    grpc.StatusCode.INVALID_ARGUMENT,
    grpc.StatusCode.UNIMPLEMENTED,
    grpc.StatusCode.RESOURCE_EXHAUSTED,
}


class TaskQueueRegistrationMismatchError(RuntimeError):
    def __init__(self, requested_task_queue: str, registered_task_queue: str) -> None:
        super().__init__(
            f"Server registered the Worker task stream on task queue "
            f"{registered_task_queue!r} instead of the requested task queue "
            f"{requested_task_queue!r}"
        )
        self.requested_task_queue = requested_task_queue
        self.registered_task_queue = registered_task_queue


def generate_app_worker_id() -> str:
    identity = next(
        (
            value
            for value in (
                os.getenv("DEX_WORKER_INSTANCE"),
                os.getenv("POD_NAME"),
                os.getenv("HOSTNAME"),
                socket.gethostname(),
            )
            if value and value.strip()
        ),
        "worker",
    )
    cleaned = re.sub(r"[^a-z0-9-]+", "-", identity.lower())[:54].strip("-")
    suffix = base64.b32encode(os.urandom(5)).decode("ascii").rstrip("=").lower()
    return f"{cleaned or 'worker'}-{suffix}"


# The Server never compresses poll responses; identity only avoids unbounded inflation.
_IDENTITY_COMPRESSION_ONLY_OPTIONS = (
    ("grpc.compression_enabled_algorithms_bitset", 1),
)


def outbound_worker_task_channel(
    server_address: str,
    transport: FlowServiceTransport | None = None,
    grpc_max_message_bytes: int = 0,
) -> grpc.aio.Channel:
    options = _IDENTITY_COMPRESSION_ONLY_OPTIONS + grpc_message_size_channel_options(
        grpc_max_message_bytes
    )
    if transport is not None and transport.credentials is not None:
        return grpc.aio.secure_channel(
            transport.target,
            transport.credentials,
            options=options + transport.channel_options,
        )
    return grpc.aio.insecure_channel(server_address, options=options)


def resolve_app_worker_id(registry: Registry, options: PullWorkerOptions) -> str:
    app_worker_id = options.app_worker_id or generate_app_worker_id()
    if app_worker_id in registry._registered_flows:
        raise ValueError(f"app_worker_id conflicts with Flow type {app_worker_id!r}")
    return app_worker_id


class PullWorkerRuntime:
    def __init__(
        self,
        registry: Registry,
        dispatcher: Any,
        options: PullWorkerOptions,
        server_address: str,
        executor: ThreadPoolExecutor | None,
        app_worker_id: str | None = None,
        credential_runner: AsyncCredentialRunner | None = None,
        transport: FlowServiceTransport | None = None,
        grpc_max_message_bytes: int = 0,
    ) -> None:
        self._transport = transport
        self._grpc_max_message_bytes = grpc_max_message_bytes
        self._registry = registry
        self.app_worker_id = app_worker_id or resolve_app_worker_id(registry, options)
        self.flow_types = sorted(registry._registered_flows)
        self.task_queue = options.task_queue
        self._stop_on_permanent_stream_rejection = (
            options.stop_on_permanent_stream_rejection
        )
        self._permanent_error: RuntimeError | None = None
        self._rejection_stop_task: asyncio.Task[None] | None = None
        self._streams: set[PullStream] = set()
        self._stopping: asyncio.Event | None = None
        self._loop: asyncio.AbstractEventLoop | None = None
        self._stub: Any = None
        self._slot_streams = PullSlotStreams(self, options)
        self._should_warn_rpc_capacity = (
            options.reserved_rpc_tasks is None and self.max_concurrent_tasks == 1
        )
        self._should_warn_floor_rpc_capacity = (
            options.reserved_rpc_tasks is None
            and self.max_concurrent_tasks >= 2
            and self._slot_streams.min_open_slots == 1
        )
        self.reserved_rpc_declines = 0
        self.queues = (STEP_AND_RPC_QUEUE,) + (
            (RPC_ONLY_QUEUE,) if self.reserved_rpc_tasks else ()
        )
        self.registration = _PullRegistration(self.queues, self.task_queue)
        self.stop_drain_timeout = options.stop_drain_timeout.total_seconds() or 30
        self._dispatcher = dispatcher
        self._server_address = server_address
        self._executor = executor
        self._credential_runner = credential_runner
        self._loop_thread_id: int | None = None
        self._started = threading.Event()
        self._wake: asyncio.Event | None = None
        self._stop_completed: asyncio.Event | None = None
        self._stop_drain_deadline: float | None = None
        self._final_output_flush_deadline: float | None = None
        self._busy_tasks = 0
        self._unsupported_logged = False

    @property
    def max_concurrent_tasks(self) -> int:
        """Return the slot ceiling, clamped to the Server cap after the first registration."""
        return self._slot_streams.max_concurrent_tasks

    @property
    def reserved_rpc_tasks(self) -> int:
        """Return the configured RPC-only slot count, derived again after a clamp."""
        return self._slot_streams.reserved_rpc_tasks

    def slot_stream_state(self) -> PullSlotStreamState:
        """Return a snapshot of the slot streams; safe from any thread."""
        return self._slot_streams.slot_stream_state()

    def unknown_task_reason(
        self, task: pb.PollWorkerTask
    ) -> pb.PollWorkerTaskRejectReason | None:
        kind = task.WhichOneof("request")
        if kind == "rpc":
            flow = self._registry._registered_flows.get(task.rpc.flow_type)
            if flow is None or task.rpc.rpc_name not in flow.rpcs:
                return pb.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_RPC
        elif kind in ("wait_for", "execute"):
            request = task.wait_for if kind == "wait_for" else task.execute
            flow = self._registry._registered_flows.get(request.flow_type)
            if flow is None:
                return pb.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
            if kind == "execute" and request.step_type == "sys:timeout_handler":
                if not flow.has_timeout_handler:
                    return pb.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
            elif request.step_type not in flow.steps:
                return pb.POLL_WORKER_TASK_REJECT_REASON_UNKNOWN_STEP
        return None

    async def run(self) -> None:
        if self._should_warn_rpc_capacity:
            _LOGGER.warning(
                "RPCs may wait behind long-running Steps; use max_concurrent_tasks >= 2 or set reserved_rpc_tasks explicitly"
            )
        if self._should_warn_floor_rpc_capacity:
            _LOGGER.warning(
                "RPCs may wait behind long-running Steps; min_open_slots 1 reserves no RPC slot, so use min_open_slots >= 2 to reserve one"
            )
        self._loop = asyncio.get_running_loop()
        self._loop_thread_id = threading.get_ident()
        self._wake = asyncio.Event()
        self._stopping = asyncio.Event()
        self._stop_completed = asyncio.Event()
        self._started.set()
        channel: grpc.aio.Channel | None = None
        try:
            channel = outbound_worker_task_channel(
                self._server_address, self._transport, self._grpc_max_message_bytes
            )
            stub = dex_pb2_grpc.WorkerTaskServiceStub(channel)  # type: ignore[no-untyped-call]
            self._stub = stub
            self._slot_streams.start_slot_streams()
            while not self._stopping.is_set():
                await self._wake.wait()
                self._wake.clear()
                self._reconcile(stub)
            await self._stop_completed.wait()
            await asyncio.gather(
                *(stream.run_task for stream in tuple(self._streams)),
                return_exceptions=True,
            )
            if self._rejection_stop_task is not None:
                await self._rejection_stop_task
            if self._permanent_error is not None:
                raise self._permanent_error
        finally:
            try:
                if channel is not None:
                    await channel.close(grace=None)
            finally:
                self._loop_thread_id = None

    def signal(self) -> None:
        if self._wake is not None:
            self._wake.set()

    def _reconcile(self, stub: Any) -> None:
        if self._stopping is None or self._stopping.is_set():
            return
        self._stub = stub
        self._slot_streams.reconcile()

    def _create_slot_stream(self, slot_kind: pb.PollWorkerSlotKind) -> PullStream:
        return PullStream(self, self._stub, slot_kind)

    def _launch_slot_stream(self, stream: PullStream) -> None:
        self._streams.add(stream)
        stream.run_task = asyncio.create_task(stream.run())
        stream.run_task.add_done_callback(stream.on_done)

    def _remove_rpc_only_slot_kind(self) -> None:
        # A clamp to a cap of one leaves no RPC-only slot, so readiness stops waiting for one.
        self.queues = (STEP_AND_RPC_QUEUE,)
        self.registration.remove_slot_kind(RPC_ONLY_QUEUE)

    def accept_registration(
        self,
        registered: pb.PollWorkerTaskRegistered,
        stream: PullStream | None = None,
    ) -> bool:
        """Check one Registered acknowledgement and record its stream as registered.

        With ``stream``, an accepted acknowledgement also records the stream in
        the registration state and the slot state machine, under one slot lock
        section with the first acknowledgement's cap resolution.

        Returns:
            True when the stream is recorded as registered and may run tasks.
            False after a permanent Worker error, including a failed first
            registration limit check; the caller must drain the stream.
        """
        if self._permanent_error is not None:
            return False
        failure = self._slot_streams.record_slot_registered(
            stream, registered.max_concurrent_tasks_per_app_worker
        )
        if failure is None:
            return True
        self._permanent_error = RuntimeError(failure)
        self.registration.record_fatal_error(self._permanent_error)
        self._slot_streams.stop_shrink_window()
        self._rejection_stop_task = asyncio.create_task(self.stop())
        return False

    def _stream_done(self, stream: PullStream, completed: asyncio.Task[None]) -> None:
        self._slot_streams.abandon_slot_attempt(stream)
        self._streams.discard(stream)
        if not completed.cancelled():
            failure = completed.exception()
            if failure is not None:
                _LOGGER.error("Worker task stream failed", exc_info=failure)
        self.signal()

    def accept_task(self) -> bool:
        if self.is_stopping or self._permanent_error is not None:
            return False
        self._busy_tasks += 1
        return True

    def finish_task(self) -> None:
        self._busy_tasks -= 1
        self.signal()

    async def stop(self) -> None:
        self.registration.record_stopping()
        if self._stopping is None:
            return
        if self._stopping.is_set():
            if self._stop_completed is not None:
                await self._stop_completed.wait()
            return
        if self._stop_completed is None:
            self._stop_completed = asyncio.Event()
        self._stop_drain_deadline = (
            asyncio.get_running_loop().time() + self.stop_drain_timeout
        )
        # The Server may not have read a final output yet; resetting would drop it.
        self._final_output_flush_deadline = (
            self._stop_drain_deadline + _FINAL_OUTPUT_FLUSH_GRACE_SECONDS
        )
        self._stopping.set()
        self._slot_streams.stop_shrink_window()
        try:
            for stream in tuple(self._streams):
                stream.drain()
            self.signal()
            active = [
                stream.handler_task for stream in self._streams if stream.handler_task
            ]
            pending: set[asyncio.Task[None]] = set()
            if active:
                _, pending = await asyncio.wait(
                    active, timeout=self._remaining_stop_drain_time()
                )
                for stream in tuple(self._streams):
                    if (
                        stream.handler_task in pending
                        and not stream.has_unconfirmed_final_output
                    ):
                        stream.close()
                        stream.cancel_handler()
            stream_tasks = [stream.run_task for stream in tuple(self._streams)]
            if stream_tasks:
                await asyncio.wait(
                    stream_tasks, timeout=self._remaining_stop_drain_time()
                )
                for stream in tuple(self._streams):
                    if (
                        not stream.run_task.done()
                        and not stream.has_unconfirmed_final_output
                    ):
                        stream.close()
                await asyncio.wait(
                    stream_tasks, timeout=self._remaining_final_output_flush_time()
                )
                for stream in tuple(self._streams):
                    if not stream.run_task.done():
                        stream.close()
                        stream.cancel_handler()
                await asyncio.gather(*stream_tasks, return_exceptions=True)
            await asyncio.gather(*pending, return_exceptions=True)
        finally:
            self._stop_completed.set()

    def _remaining_final_output_flush_time(self) -> float:
        if self._final_output_flush_deadline is None:
            raise RuntimeError("pull Worker final output flush has not started")
        return max(
            0, self._final_output_flush_deadline - asyncio.get_running_loop().time()
        )

    def _remaining_stop_drain_time(self) -> float:
        if self._stop_drain_deadline is None:
            raise RuntimeError("pull Worker Stop drain has not started")
        return max(0, self._stop_drain_deadline - asyncio.get_running_loop().time())

    def stop_threadsafe(self) -> None:
        if self.is_loop_thread():
            raise RuntimeError("pull Worker.stop() must be called from another thread")
        if not self._started.wait(timeout=10):
            raise RuntimeError("pull Worker did not start its event loop")
        if self._loop is not None and self._loop.is_running():
            asyncio.run_coroutine_threadsafe(self.stop(), self._loop).result()

    def is_loop_thread(self) -> bool:
        return self._loop_thread_id == threading.get_ident()

    @property
    def is_stopping(self) -> bool:
        return self._stopping is not None and self._stopping.is_set()


class PullStream:
    def __init__(
        self, runtime: PullWorkerRuntime, stub: Any, slot_kind: pb.PollWorkerSlotKind
    ) -> None:
        self.runtime = runtime
        self.stub = stub
        self.slot_kind = slot_kind
        self.queue = pb.PollWorkerSlotKind.Name(slot_kind).removeprefix(
            "POLL_WORKER_SLOT_KIND_"
        )
        self.run_task: asyncio.Task[None]
        self.handler_task: asyncio.Task[None] | None = None
        self.task_id = ""
        self.is_busy = False
        self._is_registered = False
        self.is_draining = False
        self._retirement_requested = asyncio.Event()
        self.is_half_closing = False
        self._active = threading.Event()
        self._call: Any = None
        self._send_lock = asyncio.Lock()
        self._keepalive_task: asyncio.Task[None] | None = None
        self._is_final_frame_sent = False
        self._is_final_output_unconfirmed = False
        self._has_send_failure = False
        self._is_reconnecting = False
        self._stream_metadata: Any = None
        self._deadline_handle: asyncio.TimerHandle | None = None
        self._is_connection_closed = False
        # The current attempt in the slot stream state machine; None for a stream it does not track.
        self.slot: _PullSlotAttempt | None = None
        self._should_retry_unauthenticated = False
        # Whether this poll registered; an orphaned handler's stream already dropped its registration count.
        self._has_registered_in_poll = False
        self._has_stop_canceled_call = False

    def on_done(self, completed: asyncio.Task[None]) -> None:
        self.runtime._stream_done(self, completed)

    async def run(self) -> None:
        backoff = 0.1
        has_retried_unauthenticated = False
        slot_streams = self.runtime._slot_streams
        while not self.runtime.is_stopping:
            # The grant may be older than the cap that another stream reported during the backoff.
            if not slot_streams.admit_slot_registration(self):
                return
            attempt = self.slot
            self.is_draining = False
            self._has_registered_in_poll = False
            was_registered = False
            is_revoked = False
            is_soft_rejection = False
            error: grpc.RpcError | None = None
            task_queue_mismatch: TaskQueueRegistrationMismatchError | None = None
            try:
                was_registered = await self._poll()
            except _SlotAttemptRevokedError:
                # The revocation already ended the attempt and re-ran the reopen rule.
                is_revoked = True
            except grpc.RpcError as failure:
                error = failure
            except TaskQueueRegistrationMismatchError as failure:
                task_queue_mismatch = failure
            except asyncio.InvalidStateError:
                if self.runtime.is_stopping and self._has_stop_canceled_call:
                    # Stop canceled this call, so a write that it interrupted ends normally.
                    _LOGGER.debug(
                        "Worker task stream write ended by stop",
                        extra={"queue": self.queue},
                    )
                else:
                    _LOGGER.exception(
                        "Worker task stream failed", extra={"queue": self.queue}
                    )
            except Exception:
                _LOGGER.exception(
                    "Worker task stream failed", extra={"queue": self.queue}
                )
            finally:
                if self._has_registered_in_poll:
                    has_retried_unauthenticated = False
                if not is_revoked:
                    is_soft_rejection = self._record_stream_result(
                        attempt, error, task_queue_mismatch, has_retried_unauthenticated
                    )
            if is_revoked:
                return
            if self.runtime.is_stopping or self.runtime._permanent_error is not None:
                break
            if is_soft_rejection:
                # A soft rejection waits for another registered stream to end or for the next shrink window.
                _LOGGER.debug(
                    "Worker task stream reached the Server stream limit: %s", error
                )
                return
            if self._should_retry_unauthenticated:
                if not slot_streams.take_reopen_grant(self):
                    return
                has_retried_unauthenticated = True
                continue
            code = error.code() if error is not None else None
            if code is grpc.StatusCode.UNAUTHENTICATED:
                has_retried_unauthenticated = False
            if self._is_reconnecting:
                self._is_reconnecting = False
                backoff = 0.1
            if was_registered:
                backoff = 0.1
            if code is grpc.StatusCode.UNIMPLEMENTED:
                if not self.runtime._unsupported_logged:
                    self.runtime._unsupported_logged = True
                    _LOGGER.error(
                        "pull Worker polling is unsupported by this Dex server"
                    )
                backoff = 60
            elif code in _PERMANENT_CODES:
                _LOGGER.error("Worker task stream rejected: %s", error)
                backoff = 60
            elif task_queue_mismatch is not None:
                _LOGGER.error("Worker task stream rejected: %s", task_queue_mismatch)
                backoff = 60
            elif error is not None:
                _LOGGER.warning("Worker task stream disconnected: %s", error)
            # Only the reopen rule continues this runner; a stream closed on purpose never reopens.
            if not slot_streams.take_reopen_grant(self):
                return
            if self.runtime._stopping is not None:
                try:
                    await asyncio.wait_for(
                        self.runtime._stopping.wait(),
                        timeout=backoff + random.uniform(0, backoff / 2),
                    )
                except asyncio.TimeoutError:
                    pass
            if code not in _PERMANENT_CODES and task_queue_mismatch is None:
                backoff = min(backoff * 2, 5)

    def _record_stream_result(
        self,
        attempt: _PullSlotAttempt | None,
        error: grpc.RpcError | None,
        task_queue_mismatch: TaskQueueRegistrationMismatchError | None,
        has_retried_unauthenticated: bool,
    ) -> bool:
        """Record one poll's end in the registration state and the slot state machine.

        Returns:
            True when the poll's attempt met a soft RESOURCE_EXHAUSTED rejection
            (F5), which records no permanent error and must not be retried by
            this runner. False otherwise.
        """
        code = error.code() if error is not None else None
        credential = (
            self.runtime._credential_runner._credential
            if self.runtime._credential_runner is not None
            else None
        )
        can_reject_token = (
            code is grpc.StatusCode.UNAUTHENTICATED
            and self._stream_metadata is not None
            and credential is not None
            and callable(getattr(credential, "_reject_token", None))
        )
        self._should_retry_unauthenticated = (
            can_reject_token and not has_retried_unauthenticated
        )
        if can_reject_token and credential is not None:
            _reject_credential_token(credential, self._stream_metadata)
        permanent_error: BaseException | None = task_queue_mismatch
        if code in _PERMANENT_CODES and not self._should_retry_unauthenticated:
            permanent_error = error
        is_soft_rejection = self.runtime._slot_streams.record_slot_stream_result(
            self, attempt, code
        )
        if is_soft_rejection:
            permanent_error = None
        rejection = self.runtime.registration.record_stream_result(
            self.queue, self._is_registered, permanent_error
        )
        self._is_registered = False
        self._stream_metadata = None
        if (
            rejection is not None
            and self.runtime._stop_on_permanent_stream_rejection
            and self.runtime._permanent_error is None
        ):
            self.runtime._permanent_error = rejection
            self.runtime._rejection_stop_task = asyncio.create_task(self.runtime.stop())
        return is_soft_rejection

    async def _poll(self) -> bool:
        if self.runtime.is_stopping:
            return False
        self._is_connection_closed = False
        self._retirement_requested.clear()
        self._is_final_frame_sent = False
        self._is_final_output_unconfirmed = False
        self._has_send_failure = False
        self._stream_metadata = None
        self._has_registered_in_poll = False
        self._has_stop_canceled_call = False
        if self.runtime._credential_runner is None:
            self._call = self.stub.PollWorkerTask()
        else:
            metadata = await async_credential_metadata(
                self.runtime._credential_runner, timeout=30.0
            )
            if self.runtime.is_stopping:
                return False
            self._stream_metadata = metadata
            self._call = self.stub.PollWorkerTask(metadata=metadata)
        was_registered = False
        reader: asyncio.Task[Any] | None = None
        retirement = asyncio.create_task(self._retirement_requested.wait())
        try:
            register = pb.PollWorkerTaskRequest(
                register=pb.PollWorkerTaskRegister(
                    queue="",
                    supports_reserved_slot_dispatch=True,
                    slot_kind=self.slot_kind,
                    app_worker_id=self.runtime.app_worker_id,
                    flow_types=self.runtime.flow_types,
                    sdk_name="python",
                    sdk_version=sdk_version(),
                    task_queue=self.runtime.task_queue,
                )
            )
            slot_streams = self.runtime._slot_streams
            before_register_send = slot_streams.settings.before_register_send_for_test
            if before_register_send is not None and self.slot is not None:
                await before_register_send(self)
            # The re-check marks the attempt as sending; the write happens after the slot lock is released.
            if not slot_streams.mark_slot_registration_sending(self):
                raise _SlotAttemptRevokedError()
            await self._send(register)
            while True:
                if reader is None:
                    reader = asyncio.create_task(self._call.read())
                waiting: set[asyncio.Task[Any]] = {reader, retirement}
                if self.handler_task is not None:
                    waiting.add(self.handler_task)
                completed, _ = await asyncio.wait(
                    waiting, return_when=asyncio.FIRST_COMPLETED
                )
                if self.handler_task in completed:
                    await self._finish_handler_or_fail_stream()
                if self.is_draining and not self.is_busy:
                    if self._is_connection_closed:
                        return was_registered
                    async with self._send_lock:
                        self._stop_keepalive()
                        self.is_half_closing = True
                        self.runtime.signal()
                        await self._call.done_writing()
                    await self._await_stream_eof(reader)
                    reader = None
                    self._is_reconnecting = not self.runtime.is_stopping
                    return was_registered
                if reader not in completed:
                    continue
                frame = reader.result()
                reader = None
                if frame is _GRPC_AIO_EOF:
                    return was_registered
                kind = frame.WhichOneof("frame")
                if not was_registered:
                    if kind == "reconnect":
                        self._is_reconnecting = True
                        return False
                    if kind != "registered":
                        raise RuntimeError("Worker task stream did not register")
                    # An older Server ignores the request field and registers the default queue.
                    if frame.registered.task_queue != self.runtime.task_queue:
                        raise TaskQueueRegistrationMismatchError(
                            self.runtime.task_queue, frame.registered.task_queue
                        )
                    was_registered = True
                    if not self.runtime.accept_registration(frame.registered, self):
                        # Half-close, not reset, so an already sent task returns unused.
                        self.drain()
                        continue
                    self._is_registered = True
                    self._has_registered_in_poll = True
                    self.runtime.signal()
                    continue
                if kind == "task":
                    # The next assignment confirms the Server received the final output.
                    self._is_final_output_unconfirmed = False
                    await self._start_task(frame.task)
                elif (
                    kind == "cancel_task" and frame.cancel_task.task_id == self.task_id
                ):
                    self.cancel_handler()
                elif kind == "ping":
                    await self._send(
                        pb.PollWorkerTaskRequest(pong=pb.PollWorkerTaskPong())
                    )
                elif kind == "reconnect":
                    slot_streams.record_slot_reconnect_requested(self)
                    self.drain()
        finally:
            self._is_connection_closed = True
            self._call.cancel()
            self.cancel_handler()
            if self.handler_task is not None:
                # The handler may outlive its stream, such as a sync handler; it then holds its slot as orphaned.
                self.runtime._slot_streams.record_slot_stream_ended(self)
                if self._is_registered:
                    self._is_registered = False
                    self.runtime.registration.record_stream_result(
                        self.queue, True, None
                    )
            async with self._send_lock:
                self._stop_keepalive()
            retirement.cancel()
            await asyncio.gather(retirement, return_exceptions=True)
            self.is_half_closing = False
            if self.handler_task is not None:
                await self._finish_handler()
            if reader is not None:
                reader.cancel()
                await asyncio.gather(reader, return_exceptions=True)
            self._call = None
        return was_registered

    async def _await_stream_eof(self, reader: asyncio.Task[Any] | None) -> None:
        loop = asyncio.get_running_loop()
        reconnect_deadline = loop.time() + self.runtime.stop_drain_timeout
        stopping = self.runtime._stopping
        if stopping is None:
            raise RuntimeError("pull Worker Stop event is unavailable")
        stop_wait: asyncio.Task[bool] | None = None
        try:
            while True:
                deadline = self._stream_eof_deadline(reconnect_deadline)
                remaining = deadline - loop.time()
                if remaining <= 0:
                    _LOGGER.warning("Worker task stream did not close after draining")
                    return
                if reader is None:
                    reader = asyncio.create_task(self._call.read())
                waiting: set[asyncio.Task[Any]] = {reader}
                if self.runtime._stop_drain_deadline is None:
                    if stop_wait is None:
                        stop_wait = asyncio.create_task(stopping.wait())
                    waiting.add(stop_wait)
                completed, _ = await asyncio.wait(
                    waiting, timeout=remaining, return_when=asyncio.FIRST_COMPLETED
                )
                if not completed:
                    _LOGGER.warning("Worker task stream did not close after draining")
                    return
                if stop_wait in completed:
                    await stop_wait
                    stop_wait = None
                    continue
                frame = await reader
                reader = None
                if frame is _GRPC_AIO_EOF:
                    return
                if frame.WhichOneof("frame") == "task":
                    self._is_final_output_unconfirmed = False
        finally:
            if stop_wait is not None:
                stop_wait.cancel()
                try:
                    await stop_wait
                except asyncio.CancelledError:
                    pass
            if reader is not None:
                reader.cancel()
                try:
                    await reader
                except asyncio.CancelledError:
                    pass

    def _stream_eof_deadline(self, reconnect_deadline: float) -> float:
        # An unread final output gets the flush grace; a reset could drop it.
        stop_drain_deadline = self.runtime._stop_drain_deadline
        flush_deadline = self.runtime._final_output_flush_deadline
        if stop_drain_deadline is None or flush_deadline is None:
            if self.has_unconfirmed_final_output:
                return reconnect_deadline + _FINAL_OUTPUT_FLUSH_GRACE_SECONDS
            return reconnect_deadline
        if self.has_unconfirmed_final_output:
            return flush_deadline
        return stop_drain_deadline

    async def _start_task(self, task: pb.PollWorkerTask) -> None:
        if self.runtime.is_stopping or self.is_draining:
            self.runtime._slot_streams.record_slot_task_dropped(self)
            return
        if self.is_busy:
            if not self._is_final_frame_sent:
                raise RuntimeError(
                    "Server assigned more than one task to a reserved execution slot"
                )
            # The final output frees the slot on the Server before local cleanup ends.
            await self._finish_handler_or_fail_stream()
            if self.runtime.is_stopping or self.is_draining:
                self.runtime._slot_streams.record_slot_task_dropped(self)
                return
        # Validate untrusted task fields before admission; a failure resets the stream.
        deadline_seconds = _task_deadline_seconds(task)
        unknown_reason = self.runtime.unknown_task_reason(task)
        if unknown_reason is not None:
            await self._reject_task(task, unknown_reason)
            return
        if not self.runtime.accept_task():
            return
        self.is_busy = True
        self.task_id = task.task_id
        self._active.set()
        self._is_final_frame_sent = False
        self._keepalive_task = asyncio.create_task(self._send_busy_keepalives())
        self.handler_task = asyncio.create_task(self._execute(task))
        if deadline_seconds is not None:
            self._deadline_handle = asyncio.get_running_loop().call_later(
                deadline_seconds, self._cancel_if_current, task.task_id
            )
        self.runtime._slot_streams.record_slot_task_started(self)
        self.runtime.signal()

    async def _reject_task(
        self,
        task: pb.PollWorkerTask,
        reason: pb.PollWorkerTaskRejectReason,
    ) -> None:
        await self._send(
            pb.PollWorkerTaskRequest(
                reject=pb.PollWorkerTaskReject(
                    task_id=task.task_id,
                    reason=reason,
                )
            )
        )

    def _cancel_if_current(self, task_id: str) -> None:
        if self.task_id == task_id:
            self.cancel_handler()

    async def _execute(self, task: pb.PollWorkerTask) -> None:
        kind = task.WhichOneof("request")
        try:
            if self.runtime._executor is None:
                await self._execute_async(task, kind)
            else:
                await asyncio.get_running_loop().run_in_executor(
                    self.runtime._executor, self._execute_sync, task, kind
                )
        except asyncio.CancelledError:
            await self._send_error(
                task.task_id,
                asyncio.CancelledError("Worker task canceled"),
                grpc.StatusCode.CANCELLED,
            )
        except BaseException as failure:
            _LOGGER.exception("Python Worker invocation failed")
            status = grpc.StatusCode.CANCELLED if not self._active.is_set() else None
            await self._send_error(task.task_id, failure, status)

    async def _execute_async(self, task: pb.PollWorkerTask, kind: str | None) -> None:
        deadline = (
            task.deadline.ToDatetime(tzinfo=timezone.utc)
            if task.HasField("deadline")
            else None
        )
        if kind == "wait_for":
            async for output in self.runtime._dispatcher.invoke_wait_for(
                task.wait_for,
                self._active.is_set,
                deadline,
            ):
                await self._send_frame(
                    pb.PollWorkerTaskFrame(task_id=task.task_id, wait_for=output)
                )
        elif kind == "execute":
            async for output in self.runtime._dispatcher.invoke_execute(
                task.execute,
                self._active.is_set,
                deadline,
            ):
                await self._send_frame(
                    pb.PollWorkerTaskFrame(task_id=task.task_id, execute=output)
                )
        elif kind == "rpc":
            output = await self.runtime._dispatcher.invoke_rpc(
                task.rpc, self._active.is_set
            )
            await self._send_frame(
                pb.PollWorkerTaskFrame(task_id=task.task_id, rpc=output)
            )
        else:
            raise ValueError(f"unsupported Worker task request {kind}")

    def _execute_sync(self, task: pb.PollWorkerTask, kind: str | None) -> None:
        deadline = (
            task.deadline.ToDatetime(tzinfo=timezone.utc)
            if task.HasField("deadline")
            else None
        )
        if kind == "wait_for":
            outputs = self.runtime._dispatcher.invoke_wait_for(
                task.wait_for,
                self._active.is_set,
                deadline,
            )
            for output in outputs:
                self._send_from_thread(
                    pb.PollWorkerTaskFrame(task_id=task.task_id, wait_for=output)
                )
        elif kind == "execute":
            outputs = self.runtime._dispatcher.invoke_execute(
                task.execute,
                self._active.is_set,
                deadline,
            )
            for output in outputs:
                self._send_from_thread(
                    pb.PollWorkerTaskFrame(task_id=task.task_id, execute=output)
                )
        elif kind == "rpc":
            output = self.runtime._dispatcher.invoke_rpc(task.rpc, self._active.is_set)
            self._send_from_thread(
                pb.PollWorkerTaskFrame(task_id=task.task_id, rpc=output)
            )
        else:
            raise ValueError(f"unsupported Worker task request {kind}")

    def _send_from_thread(self, frame: pb.PollWorkerTaskFrame) -> None:
        loop = self.runtime._loop
        if loop is None:
            raise RuntimeError("pull Worker event loop has stopped")
        asyncio.run_coroutine_threadsafe(self._send_frame(frame), loop).result()

    async def _send_error(
        self,
        task_id: str,
        failure: BaseException,
        status: grpc.StatusCode | None = None,
    ) -> None:
        worker_error = pb.WorkerErrorResponse()
        report = _worker_error_status(failure)
        report.details[0].Unpack(worker_error)
        if status is not None:
            worker_error.grpc_status = status.value[0]
        try:
            await self._send_frame(
                pb.PollWorkerTaskFrame(task_id=task_id, worker_error=worker_error)
            )
        except Exception:
            _LOGGER.exception("send Worker error frame failed")

    async def _send_frame(self, frame: pb.PollWorkerTaskFrame) -> None:
        await self._send(pb.PollWorkerTaskRequest(task_frame=frame))

    async def _send_busy_keepalives(self) -> None:
        try:
            while True:
                await asyncio.sleep(_BUSY_PULL_STREAM_KEEPALIVE_SECONDS)
                async with self._send_lock:
                    if self._is_final_frame_sent or self.is_half_closing:
                        return
                    await self._call.write(
                        pb.PollWorkerTaskRequest(pong=pb.PollWorkerTaskPong())
                    )
        except asyncio.CancelledError:
            return
        except Exception:
            self._has_send_failure = True
            _LOGGER.exception("send Worker keepalive Pong failed")

    def _stop_keepalive(self) -> None:
        if self._keepalive_task is not None:
            self._keepalive_task.cancel()
            self._keepalive_task = None

    async def _send(self, request: pb.PollWorkerTaskRequest) -> None:
        if (
            request.HasField("task_frame")
            and request.task_frame.task_id == self.task_id
            and _is_final_task_frame(request.task_frame)
        ):
            self._is_final_output_unconfirmed = True
        async with self._send_lock:
            if self._is_connection_closed:
                return
            if request.HasField("task_frame"):
                frame = request.task_frame
                is_progress_frame = (
                    frame.HasField("wait_for")
                    and frame.wait_for.WhichOneof("output")
                    in ("heartbeat", "stream_write")
                    or frame.HasField("execute")
                    and frame.execute.WhichOneof("output")
                    in ("heartbeat", "stream_write")
                )
                if (
                    frame.task_id != self.task_id
                    or self._is_final_frame_sent
                    or not self._active.is_set()
                    and is_progress_frame
                ):
                    return
                if _is_final_task_frame(frame):
                    self._stop_keepalive()
                    self._is_final_frame_sent = True
            if request.HasField("pong") and (
                self._is_final_frame_sent or self.is_half_closing
            ):
                return
            try:
                await self._call.write(request)
            except BaseException:
                self._has_send_failure = True
                raise

    @property
    def has_unconfirmed_final_output(self) -> bool:
        return self._is_final_output_unconfirmed and not self._has_send_failure

    async def _finish_handler_or_fail_stream(self) -> None:
        if not await self._finish_handler() and not self._is_connection_closed:
            # A clean half-close would tell the Server the task never ran.
            raise RuntimeError("Worker task ended without sending its final output")

    async def _finish_handler(self) -> bool:
        """Reap the finished handler and release this slot.

        Returns:
            True when the handler's final output was written to this connection.
            False when no handler ran, or the final output was never written or
            its write failed; the stream must then be reset, not half-closed.
        """
        if self.handler_task is None:
            return False
        await asyncio.gather(self.handler_task, return_exceptions=True)
        async with self._send_lock:
            self._stop_keepalive()
            is_final_output_sent = (
                self._is_final_frame_sent and not self._has_send_failure
            )
            self._is_final_frame_sent = False
        if self._deadline_handle is not None:
            self._deadline_handle.cancel()
            self._deadline_handle = None
        self.handler_task = None
        self.task_id = ""
        self.is_busy = False
        self._active.clear()
        if self.runtime._slot_streams.record_slot_task_completed(
            self, self.is_draining
        ):
            # A surplus stream after a lowered cap half-closes after its final output.
            self.is_draining = True
        if self.is_draining:
            self._retirement_requested.set()
        self.runtime.finish_task()
        return is_final_output_sent

    def cancel_handler(self) -> None:
        self._active.clear()
        if self.runtime._executor is None and self.handler_task is not None:
            self.handler_task.cancel()

    def drain(self) -> None:
        self.is_draining = True
        if not self.is_busy:
            self._retirement_requested.set()
        if self._call is None:
            self.run_task.cancel()

    def _request_slot_half_close(self) -> None:
        # The poll loop half-closes once the stream is not busy, and keeps reading until the Server ends it.
        self.is_draining = True
        if not self.is_busy:
            self._retirement_requested.set()

    def close(self) -> None:
        self._is_connection_closed = True
        if self._call is not None:
            self._has_stop_canceled_call = self.runtime.is_stopping
            self._call.cancel()


def _is_final_task_frame(frame: pb.PollWorkerTaskFrame) -> bool:
    return (
        frame.HasField("rpc")
        or frame.HasField("worker_error")
        or frame.HasField("wait_for")
        and frame.wait_for.WhichOneof("output") == "result"
        or frame.HasField("execute")
        and frame.execute.WhichOneof("output") == "result"
    )


def _task_deadline_seconds(task: pb.PollWorkerTask) -> float | None:
    """Validate a task's request kind and deadline before the task is admitted.

    Returns:
        None when the task has no deadline. Otherwise the non-negative seconds
        until the deadline.

    Raises:
        RuntimeError: If the request kind is missing or the deadline is invalid.
    """
    if task.WhichOneof("request") not in ("rpc", "wait_for", "execute"):
        raise RuntimeError(f"Worker task {task.task_id!r} has no supported request")
    if not task.HasField("deadline"):
        return None
    try:
        deadline = task.deadline.ToDatetime(tzinfo=timezone.utc)
        return max(0.0, (deadline - datetime.now(timezone.utc)).total_seconds())
    except (ValueError, OverflowError) as failure:
        raise RuntimeError(
            f"Worker task {task.task_id!r} has an invalid deadline"
        ) from failure
