# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

"""Real-server harness for the adaptive pull Worker slot stream tests.

It mirrors the Go harness: one dex-server-zd process per test, a gRPC proxy
that observes and gates slot stream registrations, and a raw competitor that
holds node stream slots under another app Worker ID.
"""

from __future__ import annotations

import asyncio
import logging
import os
import queue
import signal
import socket
import subprocess
import threading
import time
from collections.abc import Awaitable, Callable, Iterator
from dataclasses import dataclass, field
from datetime import timedelta
from pathlib import Path
from typing import Any, cast

import grpc
import grpc.aio
from google.protobuf.empty_pb2 import Empty

from dex import (
    BlobCacheConfig,
    Client,
    ClientOptions,
    Context,
    Flow,
    FlowStatus,
    LongPollTimeoutError,
    PullSlotStreamState,
    PullWorkerOptions,
    Registry,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    Worker,
    WorkerOptions,
    graceful_complete,
    open_blob_cache,
    rpc,
)
from dex._pull_slot_streams import _PullSlotSettings
from dex._pull_worker import PullStream
from dex._server_protocol import sdk_version
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc

STEP_AND_RPC = pb.POLL_WORKER_SLOT_KIND_STEP_AND_RPC
RPC_ONLY = pb.POLL_WORKER_SLOT_KIND_RPC_ONLY
POLL_METHOD = "/dex.WorkerTaskService/PollWorkerTask"
PROXY_RESET_MESSAGE = "pull slot proxy reset the stream"
MODE_BLOCK = "block"
MODE_STUBBORN = "stubborn"
MODE_QUICK = "quick"
STUBBORN_RPC_KEY = "rpc"
_EOF = getattr(grpc.aio, "EOF")
_FORWARDED_METADATA_EXCLUDED = {"user-agent", "content-type", "te", "grpc-timeout"}


def eventually(
    condition: Callable[[], bool],
    timeout: float,
    message: str | Callable[[], str] = "condition not reached",
    interval: float = 0.01,
) -> None:
    """Poll condition until it holds or the timeout expires."""
    deadline = time.monotonic() + timeout
    waiter = threading.Event()
    while True:
        if condition():
            return
        if time.monotonic() >= deadline:
            raise AssertionError(message() if callable(message) else message)
        waiter.wait(interval)


def never(
    condition: Callable[[], bool],
    duration: float,
    message: str | Callable[[], str] = "condition held",
    interval: float = 0.02,
) -> None:
    """Fail if condition holds at any poll during duration."""
    deadline = time.monotonic() + duration
    waiter = threading.Event()
    while time.monotonic() < deadline:
        if condition():
            raise AssertionError(message() if callable(message) else message)
        waiter.wait(interval)


@dataclass(frozen=True)
class ServerLimits:
    """Matching limits of a test server; zero keeps the server default."""

    max_concurrent_tasks_cap: int = 0
    max_streams: int = 0


class AdaptiveSlotTestServer:
    """One dex-server-zd process per test, so a test can set caps and restart it."""

    def __init__(self, directory: Path, limits: ServerLimits) -> None:
        binary = os.environ.get("DEX_SERVER_ZD_BINARY")
        if not binary:
            raise RuntimeError(
                "DEX_SERVER_ZD_BINARY must name a dex-server-zd binary; run ./run-integration-tests.sh"
            )
        self._binary = binary
        self._directory = directory
        self._log_path = directory / "server.log"
        self.address = ""
        self._process: subprocess.Popen[bytes] | None = None
        self.launch(limits)

    def launch(self, limits: ServerLimits) -> None:
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        self.address = f"127.0.0.1:{port}"
        configuration = (
            "dev:\n  durabilityMode: processCrashSafe\nmatching:\n  pingInterval: 2s\n"
        )
        if limits.max_concurrent_tasks_cap:
            configuration += (
                f"  maxConcurrentTasksCap: {limits.max_concurrent_tasks_cap}\n"
            )
        if limits.max_streams:
            configuration += f"  maxStreams: {limits.max_streams}\n"
        configuration_path = self._directory / "adaptive-slot-config.yaml"
        configuration_path.write_text(configuration)
        with self._log_path.open("ab") as log:
            self._process = subprocess.Popen(
                [
                    self._binary,
                    "dev",
                    "--data-dir",
                    str(self._directory / "data"),
                    "--listen",
                    self.address,
                    "--config",
                    str(configuration_path),
                ],
                stdin=subprocess.DEVNULL,
                stdout=log,
                stderr=subprocess.STDOUT,
                start_new_session=True,
            )
        self._wait_until_serving()

    def _wait_until_serving(self) -> None:
        process = self._process
        if process is None:
            raise RuntimeError("dex-server-zd was not launched")
        with grpc.insecure_channel(self.address) as channel:
            service = dex_pb2_grpc.FlowServiceStub(channel)  # type: ignore[no-untyped-call]
            deadline = time.monotonic() + 60
            waiter = threading.Event()
            while True:
                try:
                    service.GetServerInfo(Empty(), timeout=1)
                    return
                except grpc.RpcError:
                    if process.poll() is not None or time.monotonic() >= deadline:
                        raise RuntimeError(
                            "dex-server-zd did not serve:\n" + self.log_tail()
                        ) from None
                    waiter.wait(0.05)

    def kill(self) -> None:
        """End the server like a crash: every stream breaks and nothing drains."""
        process = self._process
        if process is None or process.poll() is not None:
            return
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=30)

    def restart(self, limits: ServerLimits) -> None:
        """Start a new process on the same data directory, on a new address."""
        self.launch(limits)

    def stop(self) -> None:
        process = self._process
        if process is None or process.poll() is not None:
            return
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            return
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.kill()

    def log_tail(self) -> str:
        if not self._log_path.exists():
            return ""
        lines = self._log_path.read_text(errors="replace").splitlines()
        return "\n".join(lines[-200:])


@dataclass(frozen=True)
class ProxyRegister:
    app_worker_id: str
    slot_kind: int
    at: float


@dataclass(eq=False)
class _ProxyStream:
    app_worker_id: str
    slot_kind: int
    is_registered: bool = False
    has_ended: bool = False
    is_reset: bool = False
    stage: str = "arrived"
    upstream_answered: asyncio.Event = field(default_factory=asyncio.Event)
    reset_requested: asyncio.Event = field(default_factory=asyncio.Event)


@dataclass
class _ProxyGate:
    release: asyncio.Event = field(default_factory=asyncio.Event)
    is_released_by_rpc_only: bool = False
    held: list[_ProxyStream] = field(default_factory=list)
    # Set when the first STEP_AND_RPC Register waits; an RPC_ONLY Register waits for it.
    step_held: asyncio.Event = field(default_factory=asyncio.Event)


# The grpc stubs differ across Python versions, so the proxy types its gRPC objects as Any.
_GenericRpcHandler: Any = grpc.GenericRpcHandler


class _ProxyHandler(_GenericRpcHandler):  # type: ignore[misc]
    def __init__(self, proxy: PullSlotProxy) -> None:
        self._proxy = proxy

    def service(self, handler_call_details: Any) -> Any:
        method = cast(str, getattr(handler_call_details, "method"))
        metadata = tuple(getattr(handler_call_details, "invocation_metadata") or ())

        async def forward_poll(_request: Any, context: Any) -> None:
            await self._proxy._forward_poll(context, metadata)

        async def forward_other(_request: Any, context: Any) -> None:
            await self._proxy._forward_other(method, context, metadata)

        behavior = forward_poll if method == POLL_METHOD else forward_other
        return grpc.stream_stream_rpc_method_handler(behavior)


class PullSlotProxy:
    """Forward a Worker's gRPC traffic to the current server and observe its slot streams."""

    def __init__(self, upstream_address: str) -> None:
        self._lock = threading.Lock()
        self._loop = asyncio.new_event_loop()
        self._thread = threading.Thread(
            target=self._loop.run_forever, name="pull-slot-proxy", daemon=True
        )
        self._thread.start()
        self._streams: set[_ProxyStream] = set()
        self._registers: list[ProxyRegister] = []
        self._acknowledgements: list[ProxyRegister] = []
        self._rejections: list[ProxyRegister] = []
        self._worker_resets: list[ProxyRegister] = []
        self._step_register_gate: _ProxyGate | None = None
        self._acknowledgement_gate: asyncio.Event | None = None
        self._fail_step_registers = False
        self._pass_step_registers = 0
        self._half_close_gate: asyncio.Event | None = None
        self._held_half_closes: queue.Queue[None] = queue.Queue()
        self._first_acknowledgement_at = 0.0
        self._upstream: grpc.aio.Channel | None = None
        self._server: grpc.aio.Server | None = None
        self.port = self._run(self._start(upstream_address))

    @property
    def address(self) -> str:
        return f"127.0.0.1:{self.port}"

    def _run(self, coroutine: Awaitable[Any]) -> Any:
        return asyncio.run_coroutine_threadsafe(
            cast(Any, coroutine), self._loop
        ).result(timeout=30)

    def _call_soon(self, callback: Callable[[], None]) -> None:
        self._loop.call_soon_threadsafe(callback)

    async def _start(self, upstream_address: str) -> int:
        self._upstream = grpc.aio.insecure_channel(upstream_address)
        server = grpc.aio.server()
        server.add_generic_rpc_handlers((_ProxyHandler(self),))
        port = server.add_insecure_port("127.0.0.1:0")
        await server.start()
        self._server = server
        return port

    def close(self) -> None:
        async def stop() -> None:
            if self._server is not None:
                await self._server.stop(grace=None)
            if self._upstream is not None:
                await self._upstream.close(grace=None)

        try:
            self._run(stop())
        finally:
            self._loop.call_soon_threadsafe(self._loop.stop)
            self._thread.join(timeout=10)

    def set_upstream(self, address: str) -> None:
        """Point new calls at address; calls already open stay on the old server."""

        async def switch() -> None:
            previous = self._upstream
            self._upstream = grpc.aio.insecure_channel(address)
            if previous is not None:
                # The old server is gone, so its connection has nothing left to flush.
                await previous.close(grace=None)

        self._run(switch())

    async def _forward_other(
        self, method: str, context: Any, metadata: tuple[Any, ...]
    ) -> None:
        upstream: Any = self._upstream
        if upstream is None:
            await context.abort(grpc.StatusCode.UNAVAILABLE, "proxy has no upstream")
            return
        call: Any = upstream.stream_stream(method)(
            metadata=_forwarded_metadata(metadata)
        )
        pump = asyncio.create_task(_pump_downstream(context, call, None))
        try:
            while True:
                frame = await call.read()
                if frame is _EOF:
                    break
                await context.write(frame)
            code = await call.code()
            details = await call.details()
        except grpc.aio.AioRpcError as failure:
            code, details = failure.code(), failure.details() or ""
        finally:
            pump.cancel()
            await asyncio.gather(pump, return_exceptions=True)
        if code is not grpc.StatusCode.OK:
            await context.abort(code, details)

    async def _forward_poll(self, context: Any, metadata: tuple[Any, ...]) -> None:
        first = await context.read()
        if first is _EOF:
            return
        request = pb.PollWorkerTaskRequest.FromString(first)
        stream = _ProxyStream(
            request.register.app_worker_id, int(request.register.slot_kind)
        )
        register = ProxyRegister(
            stream.app_worker_id, stream.slot_kind, time.monotonic()
        )
        is_step = stream.slot_kind == STEP_AND_RPC
        with self._lock:
            self._registers.append(register)
            self._streams.add(stream)
            gate = self._step_register_gate
            if is_step and gate is not None:
                gate.held.append(stream)
                gate.step_held.set()
            should_fail = (
                is_step and self._fail_step_registers and self._pass_step_registers == 0
            )
            if is_step and self._fail_step_registers and self._pass_step_registers > 0:
                self._pass_step_registers -= 1
        call: Any = None
        is_worker_reset = False
        try:
            if should_fail:
                stream.upstream_answered.set()
                await context.abort(
                    grpc.StatusCode.UNAVAILABLE, "pull slot proxy failed the Register"
                )
                return
            if is_step and gate is not None:
                stream.stage = "gated"
                await gate.release.wait()
            # The RPC_ONLY stream registers only once a STEP_AND_RPC Register waits, so that Register meets a full cap.
            if not is_step and gate is not None and gate.is_released_by_rpc_only:
                stream.stage = "waiting for a held Step-and-RPC Register"
                await gate.step_held.wait()
            upstream: Any = self._upstream
            if upstream is None:
                raise RuntimeError("proxy has no upstream")
            stream.stage = "opening upstream"
            call = upstream.stream_stream(POLL_METHOD)(
                metadata=_forwarded_metadata(metadata)
            )
            # From here a cancellation of this handler is a reset that came from the Worker.
            is_worker_reset = True
            code, details = await self._pump_poll(stream, context, call, first)
            is_worker_reset = False
            self._record_stream_end(stream, register, code, False)
            if stream.is_reset:
                await context.abort(grpc.StatusCode.UNAVAILABLE, PROXY_RESET_MESSAGE)
            if code is not grpc.StatusCode.OK:
                await context.abort(code, details)
        except asyncio.CancelledError:
            if is_worker_reset:
                self._record_stream_end(stream, register, None, not stream.is_reset)
            raise
        finally:
            stream.upstream_answered.set()
            if call is not None:
                call.cancel()
            with self._lock:
                stream.has_ended = True
                self._streams.discard(stream)

    async def _pump_poll(
        self, stream: _ProxyStream, context: Any, call: Any, first: bytes
    ) -> tuple[grpc.StatusCode, str]:
        try:
            await call.write(first)
        except (grpc.aio.AioRpcError, asyncio.InvalidStateError):
            pass
        stream.stage = "register sent upstream"
        pump = asyncio.create_task(
            _pump_downstream(context, call, self._hold_half_close)
        )
        reset_wait = asyncio.create_task(stream.reset_requested.wait())
        try:
            while True:
                reader = asyncio.ensure_future(call.read())
                completed, _ = await asyncio.wait(
                    {reader, reset_wait}, return_when=asyncio.FIRST_COMPLETED
                )
                if reset_wait in completed:
                    reader.cancel()
                    await asyncio.gather(reader, return_exceptions=True)
                    call.cancel()
                    return grpc.StatusCode.UNAVAILABLE, PROXY_RESET_MESSAGE
                try:
                    frame = reader.result()
                except grpc.aio.AioRpcError as failure:
                    stream.upstream_answered.set()
                    return failure.code(), failure.details() or ""
                if frame is _EOF:
                    stream.upstream_answered.set()
                    code = await call.code()
                    return code, await call.details()
                stream.upstream_answered.set()
                response = pb.PollWorkerTaskResponse.FromString(frame)
                if response.HasField("registered"):
                    await self._record_acknowledgement(stream)
                await context.write(frame)
        finally:
            reset_wait.cancel()
            pump.cancel()
            await asyncio.gather(reset_wait, pump, return_exceptions=True)

    async def _record_acknowledgement(self, stream: _ProxyStream) -> None:
        acknowledgement_gate = self._acknowledgement_gate
        if acknowledgement_gate is not None:
            await acknowledgement_gate.wait()
        with self._lock:
            stream.is_registered = True
            now = time.monotonic()
            self._acknowledgements.append(
                ProxyRegister(stream.app_worker_id, stream.slot_kind, now)
            )
            if self._first_acknowledgement_at == 0.0:
                self._first_acknowledgement_at = now
            gate = self._step_register_gate
            is_releasing_step_registers = (
                gate is not None
                and gate.is_released_by_rpc_only
                and stream.slot_kind == RPC_ONLY
            )
            if is_releasing_step_registers:
                self._step_register_gate = None
        if is_releasing_step_registers and gate is not None:
            gate.release.set()
            # The Worker sees this acknowledgement only after the server answered every held Register.
            for held in gate.held:
                await asyncio.wait_for(held.upstream_answered.wait(), timeout=10)

    async def _hold_half_close(self) -> None:
        gate = self._half_close_gate
        if gate is None:
            return
        self._held_half_closes.put(None)
        await gate.wait()

    def _record_stream_end(
        self,
        stream: _ProxyStream,
        register: ProxyRegister,
        code: grpc.StatusCode | None,
        is_worker_reset: bool,
    ) -> None:
        with self._lock:
            if code is grpc.StatusCode.RESOURCE_EXHAUSTED:
                self._rejections.append(register)
            if is_worker_reset:
                self._worker_resets.append(
                    ProxyRegister(
                        stream.app_worker_id, stream.slot_kind, time.monotonic()
                    )
                )

    def hold_step_registers_until_rpc_only_acknowledged(self) -> None:
        """Force the reconnect order of design Test 17.

        A STEP_AND_RPC Register waits, the RPC_ONLY stream registers, and the
        waiting Register reaches the server before the Worker sees that
        acknowledgement.
        """
        with self._lock:
            self._step_register_gate = _ProxyGate(is_released_by_rpc_only=True)

    def hold_step_registers(self) -> Callable[[], None]:
        """Hold STEP_AND_RPC Registers until the returned release is called."""
        gate = _ProxyGate()
        with self._lock:
            self._step_register_gate = gate

        def release() -> None:
            with self._lock:
                if self._step_register_gate is gate:
                    self._step_register_gate = None
            self._call_soon(gate.release.set)

        return release

    def held_step_registers(self) -> int:
        with self._lock:
            gate = self._step_register_gate
            if gate is None:
                return 0
            return sum(not held.has_ended for held in gate.held)

    def fail_step_registers_from_now(self) -> None:
        with self._lock:
            self._fail_step_registers = True
            self._pass_step_registers = 0

    def pass_next_step_register(self) -> None:
        with self._lock:
            self._pass_step_registers += 1

    def stop_failing_step_registers(self) -> None:
        with self._lock:
            self._fail_step_registers = False

    def hold_half_closes(self) -> tuple[queue.Queue[None], Callable[[], None]]:
        """Keep every Worker half-close from reaching the server until release is called."""
        gate = asyncio.Event()
        with self._lock:
            self._half_close_gate = gate

        def release() -> None:
            with self._lock:
                self._half_close_gate = None
            self._call_soon(gate.set)

        return self._held_half_closes, release

    def hold_acknowledgements(self) -> Callable[[], None]:
        """Delay every acknowledgement until the returned release is called."""
        gate = asyncio.Event()
        with self._lock:
            self._acknowledgement_gate = gate

        def release() -> None:
            with self._lock:
                self._acknowledgement_gate = None
            self._call_soon(gate.set)

        return release

    def reset_registered_stream(self, app_worker_id: str, slot_kind: int) -> bool:
        """Reset one acknowledged stream of the kind, like a network failure."""
        with self._lock:
            for stream in self._streams:
                if (
                    stream.app_worker_id == app_worker_id
                    and stream.slot_kind == slot_kind
                    and stream.is_registered
                    and not stream.has_ended
                    and not stream.is_reset
                ):
                    stream.is_reset = True
                    self._call_soon(stream.reset_requested.set)
                    return True
        return False

    def registered_streams(self, app_worker_id: str, slot_kind: int) -> int:
        """Count the acknowledged, live streams of a kind, as the server holds them."""
        with self._lock:
            return sum(
                stream.app_worker_id == app_worker_id
                and stream.slot_kind == slot_kind
                and stream.is_registered
                and not stream.has_ended
                and not stream.is_reset
                for stream in self._streams
            )

    def registers_since(self, app_worker_id: str, since: float) -> list[ProxyRegister]:
        with self._lock:
            return _filter_registers(self._registers, app_worker_id, since)

    def acknowledgements_since(
        self, app_worker_id: str, since: float
    ) -> list[ProxyRegister]:
        with self._lock:
            return _filter_registers(self._acknowledgements, app_worker_id, since)

    def rejections_since(self, app_worker_id: str, since: float) -> list[ProxyRegister]:
        with self._lock:
            return _filter_registers(self._rejections, app_worker_id, since)

    def worker_resets_since(
        self, app_worker_id: str, since: float
    ) -> list[ProxyRegister]:
        with self._lock:
            return _filter_registers(self._worker_resets, app_worker_id, since)

    def first_acknowledgement_at(self) -> float:
        with self._lock:
            return self._first_acknowledgement_at

    def describe(self, app_worker_id: str) -> str:
        """Summarize what the proxy saw of app_worker_id, for failure messages."""
        with self._lock:
            lines = []
            for name, events in (
                ("registers", self._registers),
                ("acknowledgements", self._acknowledgements),
                ("rejections", self._rejections),
                ("worker_resets", self._worker_resets),
            ):
                matching = _filter_registers(events, app_worker_id, 0.0)
                lines.append(
                    f"{name}: "
                    + " ".join(
                        f"{_slot_kind_name(event.slot_kind)}@{event.at:.3f}"
                        for event in matching
                    )
                )
            for stream in self._streams:
                if stream.app_worker_id == app_worker_id:
                    lines.append(
                        f"live {_slot_kind_name(stream.slot_kind)} "
                        f"registered={stream.is_registered} stage={stream.stage}"
                    )
            return "\n".join(lines)


async def _pump_downstream(
    context: Any,
    call: Any,
    before_half_close: Callable[[], Awaitable[None]] | None,
) -> None:
    # Forward the client's frames and its half-close; before_half_close may delay the half-close.
    try:
        while True:
            frame = await context.read()
            if frame is _EOF:
                if before_half_close is not None:
                    await before_half_close()
                await call.done_writing()
                return
            await call.write(frame)
    except (grpc.aio.AioRpcError, asyncio.InvalidStateError, grpc.aio.UsageError):
        # The other pump reports the stream's end.
        return


def _forwarded_metadata(metadata: tuple[Any, ...]) -> tuple[tuple[str, str], ...]:
    forwarded = []
    for item in metadata:
        key, value = item[0], item[1]
        if key.startswith(":") or key in _FORWARDED_METADATA_EXCLUDED:
            continue
        forwarded.append((key, value))
    return tuple(forwarded)


def _filter_registers(
    registers: list[ProxyRegister], app_worker_id: str, since: float
) -> list[ProxyRegister]:
    return [
        register
        for register in registers
        if register.app_worker_id == app_worker_id and register.at >= since
    ]


def _slot_kind_name(slot_kind: int) -> str:
    return pb.PollWorkerSlotKind.Name(cast(Any, slot_kind)).removeprefix(
        "POLL_WORKER_SLOT_KIND_"
    )


def step_registers(registers: list[ProxyRegister]) -> list[ProxyRegister]:
    return [register for register in registers if register.slot_kind == STEP_AND_RPC]


class PullSlotCompetitor:
    """Hold node slots under another app Worker ID and retake any freed slot at once."""

    def __init__(self, address: str, app_worker_id: str, loops: int) -> None:
        self._channel = grpc.insecure_channel(address)
        self._stub = dex_pb2_grpc.WorkerTaskServiceStub(self._channel)  # type: ignore[no-untyped-call]
        self._app_worker_id = app_worker_id
        self._stopped = threading.Event()
        self._lock = threading.Lock()
        self._held: list[Any] = []
        self._threads = [
            threading.Thread(
                target=self._hold_slot,
                name=f"pull-slot-competitor-{index}",
                daemon=True,
            )
            for index in range(loops)
        ]
        for thread in self._threads:
            thread.start()

    def _hold_slot(self) -> None:
        while not self._stopped.is_set():
            self._register_and_hold()
            self._stopped.wait(0.001)

    def _register_and_hold(self) -> None:
        requests: queue.Queue[pb.PollWorkerTaskRequest | None] = queue.Queue()
        requests.put(
            pb.PollWorkerTaskRequest(
                register=pb.PollWorkerTaskRegister(
                    supports_reserved_slot_dispatch=True,
                    slot_kind=STEP_AND_RPC,
                    app_worker_id=self._app_worker_id,
                    flow_types=["pullSlotCompetitorFlow"],
                    sdk_name="python",
                    sdk_version=sdk_version(),
                )
            )
        )

        def request_iterator() -> Iterator[pb.PollWorkerTaskRequest]:
            while True:
                request = requests.get()
                if request is None:
                    return
                yield request

        call = self._stub.PollWorkerTask(request_iterator())
        is_held = False
        try:
            first = next(call)
            if not first.HasField("registered"):
                return
            with self._lock:
                if self._stopped.is_set():
                    return
                self._held.append(call)
                is_held = True
            for response in call:
                if response.HasField("ping"):
                    requests.put(pb.PollWorkerTaskRequest(pong=pb.PollWorkerTaskPong()))
        except (grpc.RpcError, StopIteration):
            return
        finally:
            requests.put(None)
            call.cancel()
            if is_held:
                with self._lock:
                    self._held.remove(call)

    def held_streams(self) -> int:
        with self._lock:
            return len(self._held)

    def close_one_stream(self) -> None:
        """End one held stream; its loop re-registers into the freed slot at once."""
        with self._lock:
            if self._held:
                self._held[0].cancel()

    def stop(self) -> None:
        if self._stopped.is_set():
            return
        self._stopped.set()
        with self._lock:
            for call in self._held:
                call.cancel()
        for thread in self._threads:
            thread.join(timeout=10)
        self._channel.close()


@dataclass(frozen=True)
class AdaptiveSlotEntry:
    flow_id: str
    kind: str
    attempt: int


class AdaptiveSlotHandlers:
    """Record handler entries and release blocked handlers."""

    def __init__(self) -> None:
        self.entered: queue.Queue[AdaptiveSlotEntry] = queue.Queue()
        self.release = threading.Event()
        self._lock = threading.Lock()
        # Releases handlers that ignore cancellation, keyed by Flow ID or "rpc".
        self._stubborn_releases: dict[str, threading.Event] = {}
        self.is_rpc_stubborn = False

    def stubborn_release(self, key: str) -> threading.Event:
        with self._lock:
            return self._stubborn_releases.setdefault(key, threading.Event())

    def release_stubborn(self, *keys: str) -> None:
        for key in keys:
            self.stubborn_release(key).set()

    def release_all_stubborn(self) -> None:
        """Let every handler that ignores cancellation return, so a failed test can stop."""
        with self._lock:
            keys = list(self._stubborn_releases)
        self.release_stubborn(*keys, STUBBORN_RPC_KEY)

    def await_entries(self, count: int, timeout: float = 60) -> list[AdaptiveSlotEntry]:
        entries: list[AdaptiveSlotEntry] = []
        deadline = time.monotonic() + timeout
        while len(entries) < count:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AssertionError(f"only {len(entries)} of {count} handlers started")
            try:
                entries.append(self.entered.get(timeout=remaining))
            except queue.Empty:
                continue
        return entries

    def drain_entries(self) -> list[AdaptiveSlotEntry]:
        entries: list[AdaptiveSlotEntry] = []
        while True:
            try:
                entries.append(self.entered.get_nowait())
            except queue.Empty:
                return entries


class AdaptiveSlotStep(Step[str]):
    def __init__(self, handlers: AdaptiveSlotHandlers) -> None:
        self.handlers = handlers

    def execute(self, context: Context, input: str) -> StepDecision:
        self.handlers.entered.put(
            AdaptiveSlotEntry(context.flow_id, "step", context.attempt)
        )
        if input == MODE_BLOCK:
            while not self.handlers.release.wait(0.05):
                if context.is_cancellation_requested():
                    raise RuntimeError("adaptive slot Step canceled")
        elif input == MODE_STUBBORN and context.attempt == 1:
            # Only the first attempt ignores cancellation, so the retry after a restart completes.
            self.handlers.stubborn_release(context.flow_id).wait()
        return graceful_complete(input)


class AdaptiveSlotFlow(Flow[str]):
    """Run one Step whose input chooses how long it holds its slot."""

    def __init__(self, handlers: AdaptiveSlotHandlers) -> None:
        self.handlers = handlers
        self.step = AdaptiveSlotStep(handlers)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.step)

    @rpc
    def get_slot_status(self, context: Context) -> RPCResult[str]:
        return RPCResult("ok")


class AdaptiveSlotParkedStep(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(Timer.by_duration(timedelta(hours=1)))

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("parked")


class AdaptiveSlotParkedFlow(Flow[None]):
    """Stay running without a slot, so RPCs can target it."""

    def __init__(self, handlers: AdaptiveSlotHandlers) -> None:
        self.handlers = handlers
        self.step = AdaptiveSlotParkedStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.step)

    @rpc
    def block_until_released(self, context: Context) -> RPCResult[str]:
        self.handlers.entered.put(
            AdaptiveSlotEntry(context.flow_id, "rpc", context.attempt)
        )
        if self.handlers.is_rpc_stubborn:
            self.handlers.stubborn_release(STUBBORN_RPC_KEY).wait()
            return RPCResult("released")
        while not self.handlers.release.wait(0.05):
            if context.is_cancellation_requested():
                raise RuntimeError("adaptive slot RPC canceled")
        return RPCResult("released")

    @rpc
    def get_slot_status(self, context: Context) -> RPCResult[str]:
        return RPCResult("ok")


class CapturedLogs(logging.Handler):
    """Record the SDK's log messages with their values."""

    def __init__(self) -> None:
        super().__init__(level=logging.DEBUG)
        self._lock_records = threading.Lock()
        self.records: list[logging.LogRecord] = []

    def emit(self, record: logging.LogRecord) -> None:
        with self._lock_records:
            self.records.append(record)

    def has_warning(self, *fragments: str) -> bool:
        return self.count_warnings(*fragments) > 0

    def count_warnings(self, *fragments: str) -> int:
        return self.count_records(logging.WARNING, *fragments)

    def count_records(self, level: int, *fragments: str) -> int:
        with self._lock_records:
            return sum(
                record.levelno == level
                and all(fragment in record.getMessage() for fragment in fragments)
                for record in self.records
            )


class AdaptiveSlotWorkerRun:
    """Own a started Worker, its thread, and its start result."""

    def __init__(
        self,
        address: str,
        handlers: AdaptiveSlotHandlers,
        flows: list[Flow[Any]],
        pull: PullWorkerOptions,
        settings: _PullSlotSettings,
        cache_directory: Path,
    ) -> None:
        self.handlers = handlers
        self.registry = Registry(flows)
        self.flows = flows
        self.cache = open_blob_cache(
            BlobCacheConfig(str(cache_directory), 64 * 1024 * 1024)
        )
        self.worker = Worker(
            self.registry,
            self.cache,
            WorkerOptions(server_address=address, pull=pull),
        )
        self.worker._pull._slot_streams.settings = settings
        self.app_worker_id = self.worker._pull.app_worker_id
        self.started = threading.Event()
        self.failure: BaseException | None = None
        self._thread = threading.Thread(
            target=self._run, name=f"adaptive-worker-{self.app_worker_id}", daemon=True
        )
        self._thread.start()

    def _run(self) -> None:
        try:
            self.worker.start()
        except BaseException as failure:
            self.failure = failure
        finally:
            self.started.set()

    def state(self) -> PullSlotStreamState:
        return self.worker.pull_slot_stream_state()

    def await_start_result(self, timeout: float) -> BaseException | None:
        if not self.started.wait(timeout):
            raise AssertionError("start did not return")
        return self.failure

    def wait_registered(self, timeout: float = 60) -> None:
        self.worker.wait_pull_registered(timeout=timeout)

    def stop(self) -> None:
        # A failed test may leave handlers that ignore cancellation; stop waits for every handler.
        self.handlers.release_all_stubborn()
        self.worker.close()
        self._thread.join(timeout=60)
        self.cache.close()


def new_client(address: str, run: AdaptiveSlotWorkerRun) -> Client:
    return Client(run.registry, run.cache, ClientOptions(address))


def start_flows(
    client: Client, flow: AdaptiveSlotFlow, mode: str, count: int
) -> list[str]:
    flow_ids = []
    for _ in range(count):
        flow_id = f"adaptive-slot-{os.urandom(8).hex()}"
        client.start_flow(flow, flow_id, mode)
        flow_ids.append(flow_id)
    return flow_ids


def require_flows_completed(
    client: Client, flow_ids: list[str], timeout: float = 120
) -> None:
    deadline = time.monotonic() + timeout
    for flow_id in flow_ids:
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise AssertionError(f"Flow {flow_id} did not complete")
            try:
                result = client.wait_for_flow(
                    flow_id, timedelta(seconds=min(10.0, max(1.0, remaining)))
                )
            except LongPollTimeoutError:
                continue
            assert result.status is FlowStatus.COMPLETED, (flow_id, result.status)
            break


def windows_to_floor(effective_ceiling: int, floor: int) -> int:
    """Invariant 5: ceil(log2(E - L + 1)) + 1 windows."""
    windows = 1
    span = 1
    while span < effective_ceiling - floor + 1:
        span *= 2
        windows += 1
    return windows


class SlotUsageSampler:
    """Record every sample where S + H + Sr + Hr exceeds C."""

    def __init__(self, run: AdaptiveSlotWorkerRun) -> None:
        self._run = run
        self._stop = threading.Event()
        self.violations: list[PullSlotStreamState] = []
        self._thread = threading.Thread(target=self._sample, daemon=True)
        self._thread.start()

    def _sample(self) -> None:
        while not self._stop.wait(0.005):
            state = self._run.state()
            usage = (
                state.open
                + state.orphaned_handlers
                + state.rpc_only_open
                + state.orphaned_rpc_only_handlers
            )
            if usage > state.total_capacity and len(self.violations) < 8:
                self.violations.append(state)

    def stop_sampling(self) -> list[PullSlotStreamState]:
        self._stop.set()
        self._thread.join(timeout=10)
        return self.violations

    def require_within_capacity(self) -> None:
        violations = self.stop_sampling()
        assert not violations, f"slot usage exceeded the capacity: {violations}"


class SnapshotInvariantSampler:
    """Read snapshots in a tight loop from another thread and record inconsistent ones.

    Each snapshot must satisfy E == max(0, C - max(R_eff, Sr + Hr)) and
    0 <= G <= E, the capacity relations that one lock section keeps together.
    """

    def __init__(self, run: AdaptiveSlotWorkerRun) -> None:
        self._run = run
        self._stop = threading.Event()
        self.violations: list[PullSlotStreamState] = []
        self.capacities: set[int] = set()
        self.sample_count = 0
        self._thread = threading.Thread(target=self._sample, daemon=True)
        self._thread.start()

    def _sample(self) -> None:
        while not self._stop.is_set():
            state = self._run.state()
            self.sample_count += 1
            self.capacities.add(state.total_capacity)
            rpc_only_usage = state.rpc_only_open + state.orphaned_rpc_only_handlers
            expected_ceiling = max(
                0, state.total_capacity - max(state.rpc_only_slots, rpc_only_usage)
            )
            is_consistent = (
                state.effective_ceiling == expected_ceiling
                and 0 <= state.growth_target <= state.effective_ceiling
            )
            if not is_consistent and len(self.violations) < 8:
                self.violations.append(state)

    def stop_sampling(self) -> list[PullSlotStreamState]:
        self._stop.set()
        self._thread.join(timeout=10)
        return self.violations


class RegisterSendBarrier:
    """Hold armed slot streams after their admission and before their Register's write."""

    def __init__(self) -> None:
        self.is_armed = threading.Event()
        self._lock = threading.Lock()
        self._held = 0
        self._passes = 0
        self._is_released = False
        self._waiters: list[tuple[asyncio.AbstractEventLoop, asyncio.Event]] = []

    @property
    def held(self) -> int:
        with self._lock:
            return self._held

    async def hold(self, stream: PullStream) -> None:
        if not self.is_armed.is_set():
            return
        loop = asyncio.get_running_loop()
        with self._lock:
            self._held += 1
        stopping = stream.runtime._stopping
        try:
            while True:
                with self._lock:
                    if self._is_released:
                        return
                    if self._passes > 0:
                        self._passes -= 1
                        return
                    changed = asyncio.Event()
                    self._waiters.append((loop, changed))
                waits = {asyncio.ensure_future(changed.wait())}
                if stopping is not None:
                    waits.add(asyncio.ensure_future(stopping.wait()))
                _, pending = await asyncio.wait(
                    waits, return_when=asyncio.FIRST_COMPLETED
                )
                for waiting in pending:
                    waiting.cancel()
                if stopping is not None and stopping.is_set():
                    return
        finally:
            with self._lock:
                self._held -= 1

    def grant_pass(self) -> None:
        with self._lock:
            self._passes += 1
            self._wake_locked()

    def release(self) -> None:
        with self._lock:
            self._is_released = True
            self._wake_locked()

    def _wake_locked(self) -> None:
        for loop, changed in self._waiters:
            if not loop.is_closed():
                loop.call_soon_threadsafe(changed.set)
        self._waiters.clear()


class BlockingRegisterStub:
    """Make the next Step-and-RPC Register writes block until their call ends or release."""

    def __init__(self, stub: Any, blocks: int) -> None:
        self._stub = stub
        self._lock = threading.Lock()
        self._remaining_blocks = blocks
        self._blocked_writes = 0
        self._is_released = False
        self._waiters: list[tuple[asyncio.AbstractEventLoop, asyncio.Event]] = []

    @property
    def blocked_writes(self) -> int:
        with self._lock:
            return self._blocked_writes

    def PollWorkerTask(self, **kwargs: Any) -> BlockingRegisterCall:
        return BlockingRegisterCall(self._stub.PollWorkerTask(**kwargs), self)

    def take_block(self) -> bool:
        with self._lock:
            if self._remaining_blocks <= 0 or self._is_released:
                return False
            self._remaining_blocks -= 1
            return True

    def release_all(self) -> None:
        with self._lock:
            self._is_released = True
            for loop, wake in self._waiters:
                if not loop.is_closed():
                    loop.call_soon_threadsafe(wake.set)
            self._waiters.clear()

    def _register_waiter(self, wake: asyncio.Event) -> bool:
        with self._lock:
            if self._is_released:
                return False
            self._waiters.append((asyncio.get_running_loop(), wake))
            self._blocked_writes += 1
            return True

    def _unregister_waiter(self, wake: asyncio.Event) -> None:
        with self._lock:
            self._blocked_writes -= 1
            self._waiters = [entry for entry in self._waiters if entry[1] is not wake]


class BlockingRegisterCall:
    """Block a Register write like a stalled transport; cancel still ends it, as in gRPC."""

    def __init__(self, call: Any, stub: BlockingRegisterStub) -> None:
        self._call = call
        self._stub = stub
        self._wake: asyncio.Event | None = None

    async def write(self, request: pb.PollWorkerTaskRequest) -> None:
        is_step_register = (
            request.HasField("register") and request.register.slot_kind == STEP_AND_RPC
        )
        if is_step_register and self._stub.take_block():
            wake = asyncio.Event()
            self._wake = wake
            if self._stub._register_waiter(wake):
                try:
                    await wake.wait()
                finally:
                    self._stub._unregister_waiter(wake)
        await self._call.write(request)

    def cancel(self) -> bool:
        if self._wake is not None:
            self._wake.set()
        return cast(bool, self._call.cancel())

    def __getattr__(self, name: str) -> Any:
        return getattr(self._call, name)
