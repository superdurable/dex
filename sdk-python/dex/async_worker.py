# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import asyncio
from types import TracebackType

import grpc
from google.protobuf import empty_pb2

from dex._async_value_hydrator import AsyncValueHydrator
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._credential_transport import AsyncCredentialRunner, async_channel
from dex._flow_service_tls import flow_service_transport
from dex._pull_worker import PullWorkerRuntime, resolve_app_worker_id
from dex._server_protocol import (
    NegotiatedServerProtocol,
    negotiate_server_protocol,
    sdk_version,
    server_info_request_error,
)
from dex._value_mapper import ValueMapper
from dex.blob_cache import BlobCache
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Registry
from dex.pull_registration import PullRegistrationState
from dex.worker_options import WorkerOptions


class AsyncWorker:
    """Host asynchronous Step and RPC handlers through outbound pull streams.

    A Worker is one-shot. Await ``start`` in a background task because it waits for
    termination, and await ``stop`` or ``close`` during shutdown. Workers need
    only an outbound connection to the Dex Server. Application handlers
    may overlap on the event loop and must avoid blocking operations.

    Attributes:
        registry: The Flow Registry served by this Worker.
        blob_cache: The shared cache used to hydrate large values.
        options: The effective WorkerOptions.

    Examples:
        >>> async with AsyncWorker(registry, cache) as worker:
        ...     task = asyncio.create_task(worker.start())
        ...     await worker.stop()
        ...     await task
    """

    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions | None = None,
    ) -> None:
        """Construct an AsyncWorker without starting its outbound pull streams.

        Args:
            registry: A Registry created with ``allow_async_handlers=True``.
            blob_cache: An open cache shared for asynchronous value hydration.
            options: Networking, startup, and bearer credential options;
                ``None`` uses defaults and sends no authorization metadata.

        Raises:
            ValueError: If the pull Worker ID or configured TLS address, server
                name, or CA bundle is invalid, or default roots cannot be loaded.
        """
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or WorkerOptions()
        transport = flow_service_transport(
            self.options.server_address, self.options.flow_service_tls
        )
        app_worker_id = resolve_app_worker_id(registry, self.options.pull)
        self._state = "created"
        self._credential_runner = (
            AsyncCredentialRunner(self.options.credential)
            if self.options.credential is not None
            else None
        )
        self._flow_channel = async_channel(
            self.options.server_address,
            self._credential_runner,
            transport=transport,
        )
        self._flow_service = dex_pb2_grpc.FlowServiceStub(  # type: ignore[no-untyped-call]
            self._flow_channel
        )
        self._sdk_version = sdk_version()
        self._server_protocol = NegotiatedServerProtocol()
        values = ValueMapper(registry.codec_registry)
        dispatcher = AsyncWorkerDispatcher(
            registry,
            values,
            AsyncValueHydrator(self._flow_service, blob_cache),
            self._server_protocol,
        )
        self._has_start_called = False
        self._pull = PullWorkerRuntime(
            registry,
            dispatcher,
            self.options.pull,
            self.options.server_address,
            None,
            app_worker_id,
            self._credential_runner,
            transport=transport,
        )
        self._stopped = asyncio.Event()

    async def __aenter__(self) -> AsyncWorker:
        """Return this Worker for asynchronous context-manager use.

        Entering does not start outbound task streams.

        Returns:
            This AsyncWorker instance.
        """
        return self

    async def __aexit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        """Close the Worker when leaving an asynchronous context block.

        Args:
            exception_type: The active exception type, if any.
            exception: The active exception value, if any.
            traceback: The active traceback, if any.
        """
        await self.close()

    async def start(self) -> None:
        """Negotiate protocol, synchronize Attribute indexes, and serve.

        ``start`` may be awaited exactly once. It contacts FlowService before
        opening outbound task streams and completes only after ``stop``
        terminates those streams.
        Permanent pull stream rejections raise PullRegistrationError when
        stop_on_permanent_stream_rejection is enabled; otherwise streams retry.

        .. versionchanged:: 1.35.0
           Requires Server protocol 4 and fails above matching.maxConcurrentTasksCap.

        Raises:
            PullRegistrationError: A permanent stream rejection stops this Worker.
            RuntimeError: If lifecycle state is invalid, Server protocol 4 is
                unsupported, concurrency exceeds matching.maxConcurrentTasksCap,
                or index synchronization fails.
        """
        if self._has_start_called:
            raise RuntimeError("Worker can only start once")
        self._has_start_called = True
        start_error: BaseException | None = None
        try:
            await self._start()
        except BaseException as failure:
            start_error = failure
            raise
        finally:
            self._pull.registration.finish_start(start_error)
            if self._state == "running":
                await self.stop()

    async def wait_pull_registered(self, timeout: float | None = None) -> None:
        """Wait until the required execution slot kinds have registered.

        Start the Worker separately before waiting, or start it concurrently.
        Readiness requires one STEP_AND_RPC stream and, when RPC reservation is
        configured, one RPC_ONLY stream. Successful registration latches after stop. Use
        pull_registration_state for subsequent readiness probes. A permanent
        rejection before readiness raises PullRegistrationError only when no
        other stream is registered for that slot kind. Startup failures wake waiters.
        When the first registration reports a Server per-Worker limit below
        ``max_concurrent_tasks``, every waiter raises that limit error and the
        Worker never becomes ready.
        Multiple callers may wait independently, including before start.

        .. versionchanged:: 1.35.0
           Readiness tracks STEP_AND_RPC and RPC_ONLY slot kinds instead of Flow queues.

        Args:
            timeout: Maximum seconds to wait; None waits indefinitely.

        Raises:
            PullRegistrationError: A slot kind is permanently rejected before readiness.
            TimeoutError: The timeout expires.
            asyncio.CancelledError: The calling task is canceled; the Worker keeps running.
            RuntimeError: The Worker stops before readiness, or
                ``max_concurrent_tasks`` exceeds the Server's
                ``matching.maxConcurrentTasksCap``.
            BaseException: The original startup failure, if start fails.

        Examples:
            >>> start_task = asyncio.create_task(worker.start())

            >>> await worker.wait_pull_registered(timeout=30)
            >>> is_ready = worker.pull_registration_state().is_ready
        """
        await self._pull.registration.wait_async(timeout)

    @property
    def pull_reserved_rpc_declines(self) -> int:
        """Return the deprecated reserved RPC decline counter, always zero.

        Reserved RPC capacity uses RPC_ONLY slots. Steps are never declined for
        capacity. This property remains available for source compatibility and
        is safe to read concurrently, including after stop.

        .. versionchanged:: 1.35.0
           Deprecated; RPC_ONLY slots replace capacity declines, so this returns zero.

        Returns:
            Zero.

        Examples:
            >>> declines = worker.pull_reserved_rpc_declines
        """
        return self._pull.reserved_rpc_declines

    def pull_registration_state(self) -> PullRegistrationState:
        """Return an immutable snapshot of execution slot kind registrations.

        Transient failures, reconnects, and stop preserve initial readiness.
        A permanent rejection makes readiness false only while that slot kind has no
        registered stream. Re-registration clears its error. Calls are safe
        concurrently with start, stop, and other waiters. See wait_pull_registered
        for an example.

        Returns:
            Current state grouped by STEP_AND_RPC and, when configured, RPC_ONLY.
        """
        return self._pull.registration.snapshot()

    async def _start(self) -> None:
        if self._state != "created":
            raise RuntimeError(f"AsyncWorker cannot start from state {self._state}")
        try:
            server_info = await self._flow_service.GetServerInfo(
                empty_pb2.Empty(),
                timeout=self.options.attribute_index_sync_timeout.total_seconds(),
            )
        except grpc.RpcError as failure:
            self._state = "stopped"
            await self._close_failed_start_channel()
            self._stopped.set()
            raise server_info_request_error(self._sdk_version, failure) from failure
        except Exception as failure:
            self._state = "stopped"
            await self._close_failed_start_channel()
            self._stopped.set()
            raise RuntimeError("cannot get Dex server info") from failure
        try:
            self._server_protocol.record_version(
                negotiate_server_protocol(server_info, self._sdk_version)
            )
        except RuntimeError:
            self._state = "stopped"
            await self._close_failed_start_channel()
            self._stopped.set()
            raise
        try:
            await self._flow_service.SyncAttributeIndexes(
                pb.SyncAttributeIndexRequest(
                    attribute_indexes=dict(self.registry._attribute_indexes)
                ),
                timeout=self.options.attribute_index_sync_timeout.total_seconds(),
            )
        except grpc.RpcError as failure:
            self._state = "stopped"
            await self._close_failed_start_channel()
            self._stopped.set()
            raise RuntimeError("cannot synchronize Attribute indexes") from failure
        except Exception as failure:
            self._state = "stopped"
            await self._close_failed_start_channel()
            self._stopped.set()
            raise RuntimeError("cannot synchronize Attribute indexes") from failure
        self._state = "running"
        await self._pull.run()
        self._stopped.set()

    async def stop(self) -> None:
        """Drain handlers and release asynchronous channel resources.

        Calls before ``start`` and repeated calls after shutdown are safe. Pull
        mode drains for ``PullWorkerOptions.stop_drain_timeout`` (30 seconds by
        default), then cancels active tasks and waits for handlers to return.
        Idle streams half-close immediately. Busy streams send final output before
        half-closing. Tasks received after stop begins are dropped without running
        handlers. A timed-out busy stream is reset before cancellation.
        Half-closed streams keep reading server frames until EOF or the remaining drain
        deadline before the channel closes. When the deadline expires, every stream
        that is still open is reset, even one blocked on a send or without Server EOF,
        so stop returns once handlers return. The Server may count a task it sent in
        that window as an attempt that may have run.
        One exception applies to a stream whose final output was queued or written
        but not yet confirmed by Server EOF or by the next assignment. Such a stream
        is not reset at the drain deadline, even if its handler finished or it
        already half-closed. It gets up to 5 more seconds to send the output,
        half-close, and receive Server EOF, and is reset after that. Stop can
        therefore return up to 5 seconds after the drain deadline.

        .. versionchanged:: 1.35.0
           Unstarted tasks return to Dex without consuming a retry.
        """
        self._pull.registration.record_stopping()
        if self._state in ("stopped", "closed"):
            return
        self._state = "stopping"
        await self._pull.stop()
        try:
            await self._flow_channel.close(None)
        finally:
            if self._credential_runner is not None:
                self._credential_runner.close()
        if self._state != "closed":
            self._state = "stopped"
        self._stopped.set()

    async def close(self) -> None:
        """Stop the AsyncWorker and permanently mark it closed.

        Uses the same shutdown contract as stop: idle streams half-close, busy
        streams send final output before half-closing, and later tasks are dropped.
        A handler exceeding the drain timeout has its stream reset.
        Pull streams finish their bounded EOF wait before the channel closes; a
        stream still open at the deadline is reset.
        Repeated calls are safe; a closed Worker cannot be started again.
        """
        try:
            await self.stop()
        finally:
            if self._credential_runner is not None:
                self._credential_runner.close()
            self._state = "closed"

    async def _close_failed_start_channel(self) -> None:
        try:
            await self._flow_channel.close(None)
        finally:
            if self._credential_runner is not None:
                self._credential_runner.close()
