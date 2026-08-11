# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from datetime import timedelta


@dataclass(frozen=True)
class WorkerTarget:
    address: str
    headless: bool = False


@dataclass(frozen=True)
class WorkerOptions:
    """Configure Worker networking and startup index synchronization.

    ``attribute_index_sync_timeout`` is the RPC deadline used before the Worker
    opens its listener. It defaults to two minutes and must be positive.
    """

    bind_address: str = ":8803"
    worker_target: WorkerTarget | None = None
    server_address: str = "localhost:8801"
    attribute_index_sync_timeout: timedelta = timedelta(minutes=2)

    def __post_init__(self) -> None:
        if self.attribute_index_sync_timeout <= timedelta(0):
            raise ValueError("attribute_index_sync_timeout must be positive")
