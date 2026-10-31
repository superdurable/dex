# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The entry point a Worker child process runs."""

from __future__ import annotations

import asyncio
import contextlib
import os
import signal
import sys
import tempfile
from collections.abc import Sequence
from datetime import timedelta
from pathlib import Path
from typing import Any

from dex import (
    AsyncWorker,
    BlobCacheConfig,
    Flow,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)

#: Environment variable carrying the Dex Server address to a Worker child.
SERVER_ADDRESS_ENVIRONMENT_VARIABLE = "DEX_AGENTS_SERVER_ADDRESS"
#: Environment variable carrying the BlobCache directory to a Worker child.
BLOB_CACHE_DIR_ENVIRONMENT_VARIABLE = "DEX_AGENTS_BLOB_CACHE_DIR"
#: Line a Worker child prints once every pull queue registered.
WORKER_READY_LINE = "dex-agents test Worker ready"
TEST_BLOB_CACHE_BYTES = 64 * 1024 * 1024
_PULL_REGISTRATION_TIMEOUT_SECONDS = 30.0


async def serve_agent_worker(
    flows: Sequence[Flow[Any]],
    *,
    server_address: str | None = None,
    blob_cache_dir: Path | None = None,
) -> None:
    """Serve Flows with an ``AsyncWorker`` until SIGTERM or SIGINT.

    The function prints ``WORKER_READY_LINE`` to stdout after every pull queue
    registered, which ``AgentWorkerProcess.start`` waits for.

    Args:
        flows: Flows to register, such as one ``AgentSessionFlow``.
        server_address: Dex Server address; ``None`` reads
            ``DEX_AGENTS_SERVER_ADDRESS``.
        blob_cache_dir: BlobCache directory; ``None`` reads
            ``DEX_AGENTS_BLOB_CACHE_DIR``, or uses a new temporary directory.

    Raises:
        KeyError: If no server address is given or set in the environment.
        PullRegistrationError: If Dex rejects a pull queue.
    """
    address = server_address or os.environ[SERVER_ADDRESS_ENVIRONMENT_VARIABLE]
    cache_dir = blob_cache_dir or Path(
        os.environ.get(BLOB_CACHE_DIR_ENVIRONMENT_VARIABLE)
        or tempfile.mkdtemp(prefix="dex-agents-worker-")
    )
    registry = Registry(list(flows), allow_async_handlers=True)
    blob_cache = open_blob_cache(BlobCacheConfig(str(cache_dir), TEST_BLOB_CACHE_BYTES))
    stop_requested = asyncio.Event()
    loop = asyncio.get_running_loop()
    for stop_signal in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(stop_signal, stop_requested.set)
    worker = AsyncWorker(
        registry,
        blob_cache,
        WorkerOptions(
            server_address=address,
            pull=PullWorkerOptions(stop_drain_timeout=timedelta(seconds=5)),
        ),
    )
    serving = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=_PULL_REGISTRATION_TIMEOUT_SECONDS)
        print(WORKER_READY_LINE, flush=True)
        stopping = asyncio.create_task(stop_requested.wait())
        await asyncio.wait({serving, stopping}, return_when=asyncio.FIRST_COMPLETED)
        stopping.cancel()
    finally:
        await worker.stop()
        with contextlib.suppress(asyncio.CancelledError):
            await serving
        blob_cache.close()


def run_agent_worker(flows: Sequence[Flow[Any]]) -> None:
    """Run ``serve_agent_worker`` as the main function of a Worker child.

    Call it from the ``__main__`` block of the module that
    ``AgentWorkerProcess`` runs.

    Example:
        >>> if __name__ == "__main__":
        ...     run_agent_worker([build_my_agent_flow()])

    Args:
        flows: Flows to register.
    """
    try:
        asyncio.run(serve_agent_worker(flows))
    except KeyboardInterrupt:
        sys.exit(130)
