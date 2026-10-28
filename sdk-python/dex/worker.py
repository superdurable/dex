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
            ValueError: If the pull Worker ID is invalid.
        """
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or WorkerOptions()
        self._credential_runner = (
            AsyncCredentialRunner(self.options.credential)
            if self.options.credential is not None
            else None
        )
        app_worker_id = resolve_app_worker_id(registry, self.options.pull)
        self._lock = threading.Lock()
        self._state = "created"
        self._flow_channel = sync_channel(
            self.options.server_address, self.options.credential
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

        Raises:
            PullRegistrationError: A permanent stream rejection stops this Worker.
            RuntimeError: If lifecycle state is invalid or index synchronization fails.
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
        """Wait until every pull queue has registered at least once.

        Start the Worker separately before waiting, or start it concurrently.
        Successful registration latches, including after stop. Use
        pull_registration_state for subsequent readiness probes. A permanent
        rejection before readiness raises PullRegistrationError only when no
        other stream is registered for that queue. Startup failures wake waiters.
        Multiple callers may wait independently, including before start.

        Args:
            timeout: Maximum seconds to wait; None waits indefinitely.

        Raises:
            PullRegistrationError: A queue is permanently rejected before readiness.
            TimeoutError: The timeout expires.
            RuntimeError: The Worker stops before readiness.
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
        """Return cumulative Step declines caused by reserved RPC capacity.

        The counter counts decline events. Dex may decline the same Step dispatch
        repeatedly while retrying it, so read the counter as a rate.
        Full-capacity rejections and unknown names are excluded. The count never
        resets during this Worker's lifetime and stays available after stop.
        Reading it alongside handlers is safe.
        Export this value to a monitoring counter to identify reserve pressure.

        Returns:
            The cumulative reserve-decline count.

        Examples:
            >>> declines = worker.pull_reserved_rpc_declines
        """
        return self._pull.reserved_rpc_declines

    def pull_registration_state(self) -> PullRegistrationState:
        """Return an immutable snapshot of pull queue registrations.

        Transient failures, reconnects, and stop preserve initial readiness.
        A permanent rejection makes readiness false only while that queue has no
        registered stream. Re-registration clears its error. Calls are safe
        concurrently with start, stop, and other waiters. See wait_pull_registered
        for an example.

        Returns:
            Current per-queue state.
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
        Half-closed pull streams wait for server EOF until the remaining drain
        deadline before the channel closes.

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
            was_created = self._state == "created"
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

        Pull streams finish their bounded EOF wait before the channel closes.
        ``close`` is idempotent; a closed Worker cannot be started again.
        """
        try:
            self.stop()
        finally:
            if self._credential_runner is not None:
                self._credential_runner.close()
            with self._lock:
                self._state = "closed"
