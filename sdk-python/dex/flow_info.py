# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from datetime import datetime
from enum import Enum
from typing import Mapping, Sequence

from dex.codec import Value


class FlowStatus(Enum):
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELED = "canceled"
    TERMINATED = "terminated"
    TIMED_OUT = "timed_out"
    CONTINUED_AS_NEW = "continued_as_new"


@dataclass(frozen=True)
class FlowInfo:
    flow_id: str
    run_id: str
    flow_type: str
    status: FlowStatus
    started_at: datetime


@dataclass(frozen=True)
class HealthInfo:
    condition: str
    hostname: str
    duration_seconds: int


@dataclass(frozen=True)
class SearchFlowEntry:
    flow_id: str
    run_id: str
    flow_type: str
    status: FlowStatus
    started_at: datetime
    closed_at: datetime | None
    attributes: Mapping[str, Value]


@dataclass(frozen=True)
class SearchFlowsPage:
    flows: Sequence[SearchFlowEntry]
    next_page_token: str
