# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Sustainable Use License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
import os
import threading
from concurrent.futures import ThreadPoolExecutor
from types import TracebackType

import grpc
from google.protobuf import empty_pb2

from dex._credential_transport import AsyncCredentialRunner, sync_channel
from dex._flow_definition_upload import (
    flow_definition_uploader,
    prepare_flow_definitions,
    upload_worker_flow_definitions,
)
from dex._flow_service_tls import flow_service_transport
from dex._pull_worker import PullWorkerRuntime, resolve_app_worker_id
from dex._server_protocol import (
    NegotiatedServerProtocol,
    negotiate_server_protocol,
    sdk_version,
    server_info_request_error,
)
from dex._value_hydrator import ValueHydrator
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex.blob_cache import BlobCache
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Registry
from dex.pull_registration import PullRegistrationState
from dex.worker_options import WorkerOptions


class Worker:
    """Host synchronous Step and RPC handlers through outbound pull streams.

    A Worker is one-shot. Call ``start`` on a dedicated thread because it blocks,
    then call ``stop`` or ``close`` during shutdown. Workers need only an
    outbound connection to the Dex Server. Handlers run concurrently in a bounded
    thread pool and must synchronize shared application state.

    Attributes:
        registry: The Flow Registry served by this Worker.
        blob_cache: The shared cache used to hydrate large values.
        options: The effective WorkerOptions.

    Examples:
        >>> worker = Worker(registry, cache)
        >>> thread = threading.Thread(target=worker.start, daemon=True)
        >>> thread.start()
        >>> worker.wait_pull_registered(timeout=30)
        >>> worker.stop()
    """

    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions | None = None,
    ) -> None:
        """Construct a Worker without starting its outbound pull streams.

        Args:
            registry: A Registry created for synchronous handlers.
            blob_cache: An open cache shared for value hydration.
            options: Networking, startup, and bearer credential options;
                ``None`` uses defaults and sends no authorization metadata.

        Raises:
            ValueError: If the pull Worker ID or configured TLS address, server
                name, or CA bundle is invalid, or default roots cannot be loaded.
                Also raised when a configured Flow definition cannot be loaded
                or two Flow definitions describe the same Flow type.

        .. versionchanged:: 1.45.0
           Loads ``flow_definition_paths`` and ``flow_definitions`` and raises
           ``ValueError`` for an invalid or duplicate Flow definition.
        """
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or WorkerOptions()
        self._flow_definitions = prepare_flow_definitions(
            self.options.flow_definition_paths, self.options.flow_definitions
        )
        transport = flow_service_transport(
            self.options.server_address, self.options.flow_service_tls
        )
        self._credential_runner = (
            AsyncCredentialRunner(self.options.credential)
            if self.options.credential is not None
            else None
        )
        app_worker_id = resolve_app_worker_id(registry, self.options.pull)
        self._lock = threading.Lock()
        self._state = "created"
        self._flow_channel = sync_channel(
            self.options.server_address,
            self.options.credential,
            transport=transport,
            grpc_max_message_bytes=self.options.grpc_max_message_bytes,
        )
        self._flow_service = dex_pb2_grpc.FlowServiceStub(  # type: ignore[no-untyped-call]
            self._flow_channel
        )
        self._sdk_version = sdk_version()
        self._server_protocol = NegotiatedServerProtocol()
        values = ValueMapper(registry.codec_registry)
        dispatcher = WorkerDispatcher(
            registry,
            values,
            ValueHydrator(self._flow_service, blob_cache),
            self._server_protocol,
        )
        concurrency = self.options.pull.max_concurrent_tasks or os.cpu_count() or 1
        self._executor = ThreadPoolExecutor(
            max_workers=concurrency,
            thread_name_prefix="dex-python-handler",
        )
        self._has_start_called = False
        self._pull = PullWorkerRuntime(
            registry,
            dispatcher,
            self.options.pull,
            self.options.server_address,
            self._executor,
            app_worker_id,
            self._credential_runner,
            transport=transport,
            grpc_max_message_bytes=self.options.grpc_max_message_bytes,
        )

    def __enter__(self) -> Worker:
        """Return this Worker for synchronous context-manager use.

        Entering does not call ``start``.

        Returns:
            This Worker instance.
        """
        return self

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        """Close the Worker when leaving a context-manager block.

        Args:
            exception_type: The active exception type, if any.
            exception: The active exception value, if any.
            traceback: The active traceback, if any.
        """
        self.close()

    def start(self) -> None:
        """Negotiate protocol, synchronize Attribute indexes, and serve.

        ``start`` may be called exactly once. It first contacts FlowService using the
        configured synchronization timeout, then starts outbound
        pull streams and waits until ``stop`` finishes. Permanent stream rejections
        raise PullRegistrationError when stop_on_permanent_stream_rejection is
        enabled; otherwise streams retry.

        .. versionchanged:: 1.35.0
           Requires Server protocol 4 and fails above matching.maxConcurrentTasksCap.

        .. versionchanged:: 1.45.0
           Uploads the configured Flow definitions before opening task streams.
           An upload failure is logged as a warning and does not fail start.

        Raises:
            PullRegistrationError: A permanent stream rejection stops this Worker.
            RuntimeError: If lifecycle state is invalid, Server protocol 4 is
                unsupported, concurrency exceeds matching.maxConcurrentTasksCap,
                or index synchronization fails.
        """
        with self._lock:
            if self._has_start_called or self._state != "created":
                raise RuntimeError(f"Worker cannot start from state {self._state}")
            self._has_start_called = True
        start_error: BaseException | None = None
        try:
            self._start()
        except BaseException as failure:
            start_error = failure
            raise
        finally:
            self._pull.registration.finish_start(start_error)
            if self._state == "running":
                self.stop()

    def wait_pull_registered(self, timeout: float | None = None) -> None:
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
            RuntimeError: The Worker stops before readiness, or
                ``max_concurrent_tasks`` exceeds the Server's
                ``matching.maxConcurrentTasksCap``.
            BaseException: The original startup failure, if start fails.

        Examples:
            >>> start_thread = threading.Thread(target=worker.start)
            >>> start_thread.start()
            >>> worker.wait_pull_registered(timeout=30)
            >>> is_ready = worker.pull_registration_state().is_ready
        """
        self._pull.registration.wait(timeout)

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

    def _start(self) -> None:
        with self._lock:
            if self._state != "created":
                raise RuntimeError(f"Worker cannot start from state {self._state}")
            try:
                server_info = self._flow_service.GetServerInfo(
                    empty_pb2.Empty(),
                    timeout=self.options.attribute_index_sync_timeout.total_seconds(),
                )
            except grpc.RpcError as failure:
                self._state = "stopped"
                self._flow_channel.close()
                self._executor.shutdown(wait=True, cancel_futures=True)
                raise server_info_request_error(self._sdk_version, failure) from failure
            except Exception as failure:
                self._state = "stopped"
                self._flow_channel.close()
                self._executor.shutdown(wait=True, cancel_futures=True)
                raise RuntimeError("cannot get Dex server info") from failure
            try:
                self._server_protocol.record_version(
                    negotiate_server_protocol(server_info, self._sdk_version)
                )
            except RuntimeError:
                self._state = "stopped"
                self._flow_channel.close()
                self._executor.shutdown(wait=True, cancel_futures=True)
                raise
            try:
                self._flow_service.SyncAttributeIndexes(
                    pb.SyncAttributeIndexRequest(
                        attribute_indexes=dict(self.registry._attribute_indexes)
                    ),
                    timeout=self.options.attribute_index_sync_timeout.total_seconds(),
                )
            except grpc.RpcError as failure:
                self._state = "stopped"
                self._flow_channel.close()
                self._executor.shutdown(wait=True, cancel_futures=True)
                raise RuntimeError("cannot synchronize Attribute indexes") from failure
            except Exception as failure:
                self._state = "stopped"
                self._flow_channel.close()
                self._executor.shutdown(wait=True, cancel_futures=True)
                raise RuntimeError("cannot synchronize Attribute indexes") from failure
            self._state = "uploading"
        # Stop closes the channel, which cancels an in-flight upload.
        upload_worker_flow_definitions(
            lambda request, timeout: self._flow_service.UploadFlowDefinitions(
                request, timeout=timeout
            ),
            self._flow_definitions,
            self.registry._registered_flows.keys(),
            flow_definition_uploader(self._sdk_version),
            self.options.attribute_index_sync_timeout.total_seconds(),
        )
        with self._lock:
            if self._state != "uploading":
                return
            self._state = "running"
        asyncio.run(self._pull.run())

    def stop(self) -> None:
        """Drain handlers and release Worker resources.

        Calls before ``start`` and repeated calls after shutdown are safe. Shutdown
        drains for ``PullWorkerOptions.stop_drain_timeout`` (30 seconds by default),
        then requests cancellation and waits for every handler to return. A
        synchronous handler must check ``Context.is_cancellation_requested`` to
        respond promptly to forced shutdown. Call ``stop`` from a thread other
        than the pull Worker's ``start`` thread.
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

        Raises:
            RuntimeError: If called on the pull Worker's ``start`` thread.
        """
        with self._lock:
            if self._state in ("stopped", "closed"):
                return
            if self._pull.is_loop_thread():
                raise RuntimeError(
                    "pull Worker.stop() must be called from another thread"
                )
            was_created = self._state in ("created", "uploading")
            self._state = "stopping"
        self._pull.registration.record_stopping()
        if not was_created:
            self._pull.stop_threadsafe()
        self._flow_channel.close()
        self._executor.shutdown(wait=True, cancel_futures=True)
        if self._credential_runner is not None:
            self._credential_runner.close()
        with self._lock:
            if self._state != "closed":
                self._state = "stopped"

    def close(self) -> None:
        """Stop the Worker and permanently mark it closed.

        Uses the same shutdown contract as stop: idle streams half-close, busy
        streams send final output before half-closing, and later tasks are dropped.
        A handler exceeding the drain timeout has its stream reset.
        Pull streams finish their bounded EOF wait before the channel closes; a
        stream still open at the deadline is reset.
        ``close`` is idempotent; a closed Worker cannot be started again.
        """
        try:
            self.stop()
        finally:
            if self._credential_runner is not None:
                self._credential_runner.close()
            with self._lock:
                self._state = "closed"
