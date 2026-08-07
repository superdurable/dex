# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
