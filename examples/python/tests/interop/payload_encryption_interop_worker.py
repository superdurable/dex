# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Serves the Python payload interop Flow until SIGINT or SIGTERM.

Run it with ``python -m tests.interop.payload_encryption_interop_worker``.
"""

from __future__ import annotations

import asyncio
import os
import signal

from dex import (
    AsyncWorker,
    BlobCacheConfig,
    PullWorkerOptions,
    Registry,
    WorkerOptions,
    open_blob_cache,
)

from tests.interop.payload_encryption_interop_flow import new_file_payload_encryption
from tests.interop.python_payload_interop_flow import PythonPayloadInteropFlow


def _required_env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        raise SystemExit(f"{name} is required")
    return value


async def serve_payload_interop_worker() -> None:
    server_address = _required_env("DEX_FLOW_SERVICE_ADDRESS")
    encryption = new_file_payload_encryption(_required_env("DEX_PAYLOAD_INTEROP_KEY_FILE"))
    cache = open_blob_cache(
        BlobCacheConfig(_required_env("DEX_PAYLOAD_INTEROP_CACHE_DIR"), 64 << 20)
    )
    worker = AsyncWorker(
        Registry((PythonPayloadInteropFlow(),), allow_async_handlers=True),
        cache,
        WorkerOptions(
            server_address=server_address,
            pull=PullWorkerOptions(max_concurrent_tasks=16),
            payload_encryption=encryption,
        ),
    )
    stop_requested = asyncio.Event()
    loop = asyncio.get_running_loop()
    for stop_signal in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(stop_signal, stop_requested.set)
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=60)
        print("payload interop worker ready: python", flush=True)
        stop_wait = asyncio.create_task(stop_requested.wait())
        await asyncio.wait({worker_task, stop_wait}, return_when=asyncio.FIRST_COMPLETED)
        stop_wait.cancel()
    finally:
        await worker.close()
        await worker_task
        cache.close()


if __name__ == "__main__":
    asyncio.run(serve_payload_interop_worker())
