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
    """Describe the lifecycle state of a Flow run.

    Attributes:
        RUNNING: The Flow can still accept work and state changes.
        COMPLETED: The Flow completed successfully.
        FAILED: The Flow ended because application or worker processing failed.
        CANCELED: The Flow cooperatively accepted a cancellation request.
        TERMINATED: The Flow was forcibly terminated.
        SERVER_SIDE_TIMEOUT_INTERNAL_ONLY: Reserved backend hard-timeout status.
        CONTINUED_AS_NEW: This run rolled forward into a new run.
    """

    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    CANCELED = "canceled"
    TERMINATED = "terminated"
    SERVER_SIDE_TIMEOUT_INTERNAL_ONLY = "server_side_timeout_internal_only"
    CONTINUED_AS_NEW = "continued_as_new"


@dataclass(frozen=True)
class FlowInfo:
    """Summarize the current or latest run of one Flow ID.

    Attributes:
        flow_id: The stable application-assigned Flow ID.
        run_id: The server-assigned run ID.
        flow_type: The registered Flow type name.
        status: The run's current lifecycle status.
        started_at: The timezone-aware server start timestamp.
    """

    flow_id: str
    run_id: str
    flow_type: str
    status: FlowStatus
    started_at: datetime


@dataclass(frozen=True)
class HealthInfo:
    """Describe one FlowService health-check response.

    Attributes:
        condition: The service-reported health condition.
        hostname: The hostname of the responding Dex server.
        duration_seconds: The server-reported response duration in seconds.
    """

    condition: str
    hostname: str
    duration_seconds: int


@dataclass(frozen=True)
class SearchFlowEntry:
    """Represent one indexed Flow run returned by ``search_flows``.

    Attributes:
        flow_id: The stable application-assigned Flow ID.
        run_id: The server-assigned run ID for this result.
        flow_type: The registered Flow type name.
        status: The indexed run status.
        started_at: The timezone-aware start timestamp.
        closed_at: The close timestamp, or ``None`` for open or unknown runs.
        indexed_attributes: Hydrated Values keyed by physical search-index name.
    """

    flow_id: str
    run_id: str
    flow_type: str
    status: FlowStatus
    started_at: datetime
    closed_at: datetime | None
    indexed_attributes: Mapping[str, Value]


@dataclass(frozen=True)
class SearchFlowsPage:
    """Contain one server-ordered page of Flow search results.

    Attributes:
        flows: The result entries in server-defined query order.
        next_page_token: An opaque token for the next request, or ``""`` on the
            final page.
    """

    flows: Sequence[SearchFlowEntry]
    next_page_token: str
