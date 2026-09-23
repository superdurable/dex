# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta
from math import ceil
from time import monotonic

import grpc

from dex.runtime_errors import ErrorSubStatus, WaitHandlerTimeoutError


@dataclass(frozen=True)
class WaitForStepCompletionOptions:
    """Configure one durable Step completion wait.

    The server derives a stable Request ID from the Step execution when
    ``request_id`` is empty. Reuse an override only for the same logical wait.
    Leave ``maximum_wait_time`` at zero for normal use. Positive values are an
    exceptional safety valve. Short budgets can add many Temporal Update events
    to Workflow history; prefer at least one minute when nonzero. A positive
    value bounds the caller-visible wait.

    Attributes:
        request_id: An optional override for the server-derived stable ID.
        maximum_wait_time: The caller-visible wait budget. Zero waits indefinitely.
            Positive values are rare; prefer at least one minute to limit Temporal
            Update history growth.
    """

    request_id: str = ""
    maximum_wait_time: timedelta = timedelta(0)


@dataclass(frozen=True)
class WaitForAttributeOptions:
    """Configure one durable Attribute match wait.

    The server derives a stable Request ID from the Attribute condition when
    ``request_id`` is empty. Reuse an override only for the same logical predicate.
    Leave ``maximum_wait_time`` at zero for normal use. Positive values are an
    exceptional safety valve. Short budgets can add many Temporal Update events
    to Workflow history; prefer at least one minute when nonzero. A positive
    value bounds the caller-visible wait.

    Attributes:
        request_id: An optional override for the server-derived stable ID.
        maximum_wait_time: The caller-visible wait budget. Zero waits indefinitely.
            Positive values are rare; prefer at least one minute to limit Temporal
            Update history growth.
    """

    request_id: str = ""
    maximum_wait_time: timedelta = timedelta(0)


class _ClientWaitBudget:
    def __init__(self, maximum_wait_time: timedelta) -> None:
        seconds = maximum_wait_time.total_seconds()
        if seconds < 0 or not seconds.is_integer() or seconds > 2_147_483_647:
            raise ValueError("duration must be whole seconds within int32")
        self._deadline = monotonic() + seconds if seconds else None

    def remaining_seconds(self, operation: str, flow_id: str) -> int:
        if self._deadline is None:
            return 0
        remaining = self._deadline - monotonic()
        if remaining <= 0:
            raise WaitHandlerTimeoutError(
                grpc.StatusCode.DEADLINE_EXCEEDED,
                ErrorSubStatus.WAIT_HANDLER_TIMEOUT,
                "wait handler timed out",
                operation,
                flow_id,
            )
        return ceil(remaining)
