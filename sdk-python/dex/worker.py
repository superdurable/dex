# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import os
import threading
from concurrent.futures import ThreadPoolExecutor
from types import TracebackType

import grpc

from dex._value_hydrator import ValueHydrator
from dex._value_mapper import ValueMapper
from dex._worker_dispatcher import WorkerDispatcher
from dex._worker_service import WorkerService
from dex.blob_cache import BlobCache
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc
from dex.flow import Registry
from dex.worker_options import WorkerOptions, WorkerTarget


class Worker:
    """Host synchronous registered Step and RPC handlers over WorkerService.

    A Worker is one-shot. Call ``start`` on a dedicated thread because it blocks,
    then call ``stop`` or ``close`` during shutdown. Handlers run concurrently in a
    bounded thread pool and must synchronize shared application state.

    Attributes:
        registry: The immutable Flow Registry served by this Worker.
        blob_cache: The shared cache used to hydrate large values.
        options: The effective WorkerOptions.

    Examples:
        >>> worker = Worker(registry, cache)
        >>> thread = threading.Thread(target=worker.start, daemon=True)
        >>> thread.start()
        >>> worker.worker_target.address
        'localhost:8803'
        >>> worker.stop()
    """

    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions | None = None,
    ) -> None:
        """Construct a Worker without starting its listener.

        Args:
            registry: A Registry created for synchronous handlers.
            blob_cache: An open cache shared for value hydration.
            options: Networking and startup options; ``None`` uses defaults.

        Raises:
            ValueError: If a configured target or bind address is malformed.
        """
        self.registry = registry
        self.blob_cache = blob_cache
        self.options = options or WorkerOptions()
        self._lock = threading.Lock()
        self._state = "created"
        self._flow_channel = grpc.insecure_channel(self.options.server_address)
        self._flow_service = dex_pb2_grpc.FlowServiceStub(  # type: ignore[no-untyped-call]
            self._flow_channel
        )
        values = ValueMapper(registry.codec_registry)
        dispatcher = WorkerDispatcher(
            registry,
            values,
            ValueHydrator(self._flow_service, blob_cache),
        )
        concurrency = max(2, min(32, os.cpu_count() or 2))
        self._executor = ThreadPoolExecutor(
            max_workers=concurrency,
            thread_name_prefix="dex-python-handler",
        )
        self._server = grpc.server(
            self._executor,
            maximum_concurrent_rpcs=concurrency,
        )
        dex_pb2_grpc.add_WorkerServiceServicer_to_server(  # type: ignore[no-untyped-call]
            WorkerService(dispatcher),
            self._server,
        )
        self._bound_port = 0
        self._worker_target = self.options.worker_target or WorkerTarget(
            self._target_address(self.options.bind_address, 0)
        )

    def __enter__(self) -> Worker:
        """Return this Worker for synchronous context-manager use.

        Entering does not call ``start``.

        Returns:
            This Worker instance.
        """
        return self

    @property
    def worker_target(self) -> WorkerTarget:
        """Return the effective endpoint advertised to Dex.

        Before ``start``, an ephemeral bind port may not yet be resolved. After
        binding, the property contains the actual selected port.

        Returns:
            The immutable advertised WorkerTarget.
        """
        return self._worker_target

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
        """Synchronize Attribute indexes, serve WorkerService, and block.

        ``start`` may be called exactly once. It first contacts FlowService using the
        configured synchronization timeout, then binds the listener and waits until
        ``stop`` shuts the server down.

        Raises:
            RuntimeError: If lifecycle state, index synchronization, or binding fails.
        """
        with self._lock:
            if self._state != "created":
                raise RuntimeError(f"Worker cannot start from state {self._state}")
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
            self._bound_port = self._server.add_insecure_port(self.options.bind_address)
            if self._bound_port == 0:
                self._state = "stopped"
                self._flow_channel.close()
                self._executor.shutdown(wait=True, cancel_futures=True)
                raise RuntimeError(
                    f"cannot bind Python Worker to {self.options.bind_address}"
                )
            if self.options.worker_target is None:
                self._worker_target = WorkerTarget(
                    self._target_address(self.options.bind_address, self._bound_port)
                )
            self._state = "running"
            self._server.start()
        self._server.wait_for_termination()

    def stop(self) -> None:
        """Drain handlers for five seconds and release Worker resources.

        Calls before ``start`` and repeated calls after shutdown are safe. Remaining
        handler futures are canceled after the gRPC grace period.
        """
        with self._lock:
            if self._state in ("stopped", "closed"):
                return
            self._state = "stopping"
        self._server.stop(grace=5).wait(timeout=10)
        self._flow_channel.close()
        self._executor.shutdown(wait=True, cancel_futures=True)
        with self._lock:
            if self._state != "closed":
                self._state = "stopped"

    def close(self) -> None:
        """Stop the Worker and permanently mark it closed.

        ``close`` is idempotent; a closed Worker cannot be started again.
        """
        self.stop()
        with self._lock:
            self._state = "closed"

    @staticmethod
    def _target_address(bind_address: str, bound_port: int) -> str:
        host, separator, port = bind_address.rpartition(":")
        if not separator or not port:
            raise ValueError("Worker bind address requires a port")
        if bound_port == 0:
            bound_port = int(port)
        if host in ("", "0.0.0.0", "::", "[::]"):
            host = "localhost"
        return f"{host}:{bound_port}"
