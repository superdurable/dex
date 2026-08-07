# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from types import TracebackType

from dex._utils import PhaseNotImplementedError
from dex.blob_cache import BlobCache
from dex.flow import Registry
from dex.worker_options import WorkerOptions


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

    def __enter__(self) -> Worker:
        return self

    def __exit__(
        self,
        exception_type: type[BaseException] | None,
        exception: BaseException | None,
        traceback: TracebackType | None,
    ) -> None:
        self.close()

    def start(self) -> None:
        raise PhaseNotImplementedError("Worker runtime belongs to a later phase")

    def stop(self) -> None:
        raise PhaseNotImplementedError("Worker runtime belongs to a later phase")

    def close(self) -> None:
        self.stop()
