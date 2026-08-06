# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dataclasses import dataclass


@dataclass(frozen=True)
class WorkerTarget:
    address: str
    headless: bool = False


@dataclass(frozen=True)
class WorkerOptions:
    bind_address: str = ":8803"
    worker_target: WorkerTarget | None = None
    server_address: str = "localhost:8801"
