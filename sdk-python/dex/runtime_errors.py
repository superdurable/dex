# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from enum import Enum
from typing import TYPE_CHECKING, Any, Literal

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


class DexServiceError(RuntimeError):
    def __init__(
        self,
        code: grpc.StatusCode,
        sub_status: ErrorSubStatus,
        detail: str,
        operation: str,
        flow_id: str | None,
    ) -> None:
        super().__init__(detail)
        self.code = code
        self.sub_status = sub_status
        self.detail = detail
        self.operation = operation
        self.flow_id = flow_id


class FlowAlreadyStartedError(DexServiceError):
    pass


class FlowNotFoundError(DexServiceError):
    pass


class FlowNotActiveError(DexServiceError):
    pass


class WorkerInvocationError(DexServiceError):
    def __init__(
        self,
        code: grpc.StatusCode,
        sub_status: ErrorSubStatus,
        detail: str,
        operation: str,
        flow_id: str | None,
        worker_code: grpc.StatusCode | None,
        worker_error_type: str,
        worker_error_detail: str,
    ) -> None:
        super().__init__(code, sub_status, detail, operation, flow_id)
        self.worker_code = worker_code
        self.worker_error_type = worker_error_type
        self.worker_error_detail = worker_error_detail


class RpcLockConflictError(DexServiceError):
    pass


class LongPollTimeoutError(DexServiceError):
    pass


class FlowDefinitionError(RuntimeError):
    pass


class InvalidStepResultError(FlowDefinitionError):
    def __init__(
        self,
        flow_type: str,
        step_type: str | None,
        method: Literal["wait_for", "execute", "rpc"],
        detail: str,
    ) -> None:
        target = (
            f"Flow {flow_type} Step {step_type}"
            if step_type is not None
            else f"RPC in Flow {flow_type}"
        )
        super().__init__(f"{target} {method} returned an invalid result: {detail}")
        self.flow_type = flow_type
        self.step_type = step_type
        self.method = method
        self.detail = detail


class ValueMappingError(RuntimeError):
    def __init__(self, operation: str, detail: str) -> None:
        super().__init__(f"Cannot {operation} Dex Value: {detail}")
        self.operation = operation
        self.detail = detail


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
