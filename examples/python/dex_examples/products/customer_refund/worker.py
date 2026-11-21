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

"""Runs the Worker for CustomerRefundFlow. Stop it with Ctrl+C."""

from __future__ import annotations

import threading
from pathlib import Path

from dex import (
    PayloadEncryptionOptions,
    PullWorkerOptions,
    Registry,
    Worker,
    WorkerOptions,
)

from dex_examples.products.customer_refund.refund_settings import (
    BILLING_MODE,
    FLOW_DEFINITIONS_DIR,
    SERVER_ADDRESS,
    new_refund_flow,
    new_worker_id,
    open_cache,
    payload_encryption,
)


def refund_worker_options(
    server_address: str, encryption: PayloadEncryptionOptions | None
) -> WorkerOptions:
    """Returns Worker options that also upload the design when it was generated."""
    design_paths = (FLOW_DEFINITIONS_DIR,) if FLOW_DEFINITIONS_DIR.is_dir() else ()
    if not design_paths:
        print(f"No design in {FLOW_DEFINITIONS_DIR}; see the README to generate it.", flush=True)
    return WorkerOptions(
        server_address=server_address,
        pull=PullWorkerOptions(app_worker_id=new_worker_id()),
        flow_definition_paths=design_paths,
        payload_encryption=encryption,
    )


def main() -> None:
    registry = Registry((new_refund_flow(),))
    cache = open_cache(Path(".dex-blob-cache/customer-refund-worker"))
    options = refund_worker_options(SERVER_ADDRESS, payload_encryption())
    worker = Worker(registry, cache, options)
    thread = threading.Thread(target=worker.start, daemon=True)
    try:
        thread.start()
        worker.wait_pull_registered(timeout=30)
        encrypted = "on" if options.payload_encryption else "off"
        print(
            f"Worker {options.pull.app_worker_id} is ready "
            f"(billing mode {BILLING_MODE}, encryption {encrypted}). Press Ctrl+C to stop it.",
            flush=True,
        )
        thread.join()
    except KeyboardInterrupt:
        print("Stopping the Worker.", flush=True)
    finally:
        worker.close()
        cache.close()


if __name__ == "__main__":
    main()
