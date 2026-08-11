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
    def __init__(
        self,
        registry: Registry,
        blob_cache: BlobCache,
        options: WorkerOptions | None = None,
    ) -> None:
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
        return self

    @property
    def worker_target(self) -> WorkerTarget:
        return self._worker_target

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()

    def start(self) -> None:
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
