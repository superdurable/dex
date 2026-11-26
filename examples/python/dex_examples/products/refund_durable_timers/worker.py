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

"""Runs the Worker for RefundDurableTimersFlow. Stop it with Ctrl+C."""

from __future__ import annotations

import asyncio
import contextlib
from pathlib import Path

from dex import AsyncWorker, PullWorkerOptions, WorkerOptions

from dex_examples.products.refund_durable_timers.refund_settings import (
    FLOW_DEFINITIONS_DIR,
    SERVER_ADDRESS,
    new_refund_registry,
    new_refund_timers_flow,
    new_worker_id,
    open_cache,
)


def refund_worker_options() -> WorkerOptions:
    """Returns Worker options that also upload the design when it was generated."""
    design_paths = (FLOW_DEFINITIONS_DIR,) if FLOW_DEFINITIONS_DIR.is_dir() else ()
    if not design_paths:
        print(f"No design in {FLOW_DEFINITIONS_DIR}; see the README to generate it.", flush=True)
    return WorkerOptions(
        server_address=SERVER_ADDRESS,
        pull=PullWorkerOptions(app_worker_id=new_worker_id()),
        flow_definition_paths=design_paths,
    )


async def run_worker() -> None:
    registry = new_refund_registry(new_refund_timers_flow())
    cache = open_cache(Path(".dex-blob-cache/refund-durable-timers-worker"))
    options = refund_worker_options()
    worker = AsyncWorker(registry, cache, options)
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=30)
        print(
            f"Worker {options.pull.app_worker_id} is ready. Press Ctrl+C to stop it.",
            flush=True,
        )
        await worker_task
    finally:
        await worker.close()
        worker_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await worker_task
        cache.close()


def main() -> None:
    with contextlib.suppress(KeyboardInterrupt):
        asyncio.run(run_worker())
    print("Stopped the Worker.", flush=True)


if __name__ == "__main__":
    main()
