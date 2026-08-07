# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from enum import Enum
from typing import TYPE_CHECKING, Any

import grpc

from dex.flow_info import FlowStatus

if TYPE_CHECKING:
    from dex._value_mapper import ValueMapper
    from dex.dexpb import dex_pb2 as pb


class ErrorSubStatus(Enum):
    UNCATEGORIZED = "uncategorized"
    FLOW_ALREADY_STARTED = "flow_already_started"
    FLOW_NOT_EXISTS = "flow_not_exists"
    WORKER_API_ERROR = "worker_api_error"
    LONG_POLL_TIMEOUT = "long_poll_timeout"


class FlowErrorType(Enum):
    STEP_DECISION_FAILED = "step_decision_failed"
    CLIENT_API_FAILED = "client_api_failed"
    WORKER_API_FAILED = "worker_api_failed"
    INVALID_USER_FLOW_CODE = "invalid_user_flow_code"
    INTERNAL = "internal"


class DexException(RuntimeError):
    def __init__(
        self,
        code: grpc.StatusCode,
        sub_status: ErrorSubStatus | None,
        detail: str | None,
        worker_error_type: str = "",
        worker_error_detail: str = "",
    ) -> None:
        super().__init__(detail)
        self.code = code
        self.sub_status = sub_status
        self.detail = detail
        self.worker_error_type = worker_error_type
        self.worker_error_detail = worker_error_detail


class LongPollTimeoutError(RuntimeError):
    def __init__(self, flow_id: str) -> None:
        super().__init__(f"Flow is still running: {flow_id}")
        self.flow_id = flow_id


class FlowUncompletedError(RuntimeError):
    def __init__(
        self,
        run_id: str,
        status: FlowStatus,
        error_type: FlowErrorType | None,
        message: str | None,
        results: list[pb.StepCompletionOutput],
        values: ValueMapper,
    ) -> None:
        super().__init__(message)
        self.run_id = run_id
        self.status = status
        self.error_type = error_type
        self.results = tuple(results)
        self._values = values

    def result(self, index: int, result_type: type[Any]) -> Any:
        value = self.results[index].completed_step_output
        return self._values.decode(value, self._values.codec(result_type))
