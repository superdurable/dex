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
    """Classify a FlowService error more precisely than its gRPC status.

    Attributes:
        UNCATEGORIZED: No stable Dex-specific classification was supplied.
        FLOW_ALREADY_STARTED: The requested Flow ID conflicts with an existing run.
        FLOW_NOT_EXISTS: No Flow with the requested ID exists.
        WORKER_API_ERROR: An application Worker rejected or failed an invocation.
        LONG_POLL_TIMEOUT: A wait ended without observing its condition.
    """

    UNCATEGORIZED = "uncategorized"
    FLOW_ALREADY_STARTED = "flow_already_started"
    FLOW_NOT_EXISTS = "flow_not_exists"
    WORKER_API_ERROR = "worker_api_error"
    LONG_POLL_TIMEOUT = "long_poll_timeout"


class FlowErrorType(Enum):
    """Identify the terminal failure category reported for a Flow.

    Attributes:
        STEP_DECISION_FAILED: A Step decision could not be applied.
        CLIENT_API_FAILED: A Client-originated operation failed the Flow.
        WORKER_API_FAILED: A Worker handler or dispatch failed.
        INVALID_USER_FLOW_CODE: Application Flow definitions or results were invalid.
        INTERNAL: Dex encountered an internal invariant or infrastructure failure.
    """

    STEP_DECISION_FAILED = "step_decision_failed"
    CLIENT_API_FAILED = "client_api_failed"
    WORKER_API_FAILED = "worker_api_failed"
    INVALID_USER_FLOW_CODE = "invalid_user_flow_code"
    INTERNAL = "internal"


class DexServiceError(RuntimeError):
    """Represent a structured error returned by a Dex FlowService operation.

    Attributes:
        code: The outer gRPC status code.
        sub_status: The stable Dex-specific error classification.
        detail: The human-readable service detail.
        operation: The Client operation that failed.
        flow_id: The targeted Flow ID, or ``None`` for service-wide calls.
    """

    def __init__(
        self,
        code: grpc.StatusCode,
        sub_status: ErrorSubStatus,
        detail: str,
        operation: str,
        flow_id: str | None,
    ) -> None:
        """Create a structured FlowService error.

        Args:
            code: The outer gRPC status code.
            sub_status: The Dex-specific classification.
            detail: Human-readable error detail.
            operation: The Client operation name.
            flow_id: The targeted Flow ID, if any.
        """
        super().__init__(detail)
        self.code = code
        self.sub_status = sub_status
        self.detail = detail
        self.operation = operation
        self.flow_id = flow_id


class FlowAlreadyStartedError(DexServiceError):
    """Indicate that ``start_flow`` conflicts with an existing Flow ID."""

    pass


class FlowNotFoundError(DexServiceError):
    """Indicate that an operation targeted a Flow ID that does not exist."""

    pass


class FlowNotActiveError(DexServiceError):
    """Indicate that an operation requires an active but already closed Flow."""

    pass


class WorkerInvocationError(DexServiceError):
    """Expose both FlowService and nested WorkerService failure details.

    Attributes:
        code: The outer FlowService gRPC status.
        sub_status: Usually ``WORKER_API_ERROR``.
        detail: The outer service detail.
        operation: The Client operation that invoked the Worker.
        flow_id: The targeted Flow ID, if available.
        worker_code: The nested Worker gRPC status, or ``None`` when unavailable.
        worker_error_type: The Worker-reported application error type.
        worker_error_detail: The Worker-reported human-readable detail.
    """

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
        """Create an error from outer and nested Worker failure metadata.

        Args:
            code: The outer FlowService gRPC status.
            sub_status: The Dex-specific classification.
            detail: The outer service detail.
            operation: The Client operation name.
            flow_id: The targeted Flow ID, if any.
            worker_code: The nested Worker gRPC status, if supplied.
            worker_error_type: The nested application error type.
            worker_error_detail: The nested human-readable detail.
        """
        super().__init__(code, sub_status, detail, operation, flow_id)
        self.worker_code = worker_code
        self.worker_error_type = worker_error_type
        self.worker_error_detail = worker_error_detail


class RpcLockConflictError(DexServiceError):
    """Indicate that an RPC could not acquire all requested Attribute locks."""

    pass


class LongPollTimeoutError(DexServiceError):
    """Indicate that a retryable long poll ended before its condition was observed."""

    pass


class FlowDefinitionError(RuntimeError):
    """Indicate that Registry construction found an invalid Flow definition."""

    pass


class InvalidStepResultError(FlowDefinitionError):
    """Identify a Step or RPC handler result that violates the SDK contract.

    Attributes:
        flow_type: The containing Flow type.
        step_type: The Step type, or ``None`` for an RPC.
        method: The handler method that returned the invalid value.
        detail: A precise description of the contract violation.
    """

    def __init__(
        self,
        flow_type: str,
        step_type: str | None,
        method: Literal["wait_for", "execute", "rpc"],
        detail: str,
    ) -> None:
        """Create an invalid-result definition error.

        Args:
            flow_type: The containing Flow type.
            step_type: The Step type, or ``None`` for an RPC.
            method: ``"wait_for"``, ``"execute"``, or ``"rpc"``.
            detail: A precise description of the invalid result.
        """
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
    """Report an application-value encoding or decoding failure.

    Attributes:
        operation: The mapping operation, such as ``"encode"`` or ``"decode"``.
        detail: The incompatible type, wire kind, or malformed payload detail.
    """

    def __init__(self, operation: str, detail: str) -> None:
        """Create a mapping error with stable operation context.

        Args:
            operation: The attempted mapping operation.
            detail: The human-readable failure detail.
        """
        super().__init__(f"Cannot {operation} Dex Value: {detail}")
        self.operation = operation
        self.detail = detail


class FlowUncompletedError(RuntimeError):
    """Report that ``wait_for_flow`` observed a non-successful terminal status.

    Completed Step outputs remain available through :meth:`result` when the request
    asked Dex to include them.

    Attributes:
        run_id: The terminal server-assigned run ID.
        status: The non-completed terminal Flow status.
        error_type: The terminal failure category, if Dex supplied one.
        results: The ordered, opaque Step completion outputs.
    """

    def __init__(
        self,
        run_id: str,
        status: FlowStatus,
        error_type: FlowErrorType | None,
        message: str | None,
        results: list[pb.StepCompletionOutput],
        values: ValueMapper,
    ) -> None:
        """Create an uncompleted-Flow error with lazy result decoding.

        Args:
            run_id: The terminal server-assigned run ID.
            status: The non-completed terminal status.
            error_type: The failure category, if supplied.
            message: Optional human-readable terminal message.
            results: Raw Step completion outputs in server order.
            values: The mapper used to decode outputs on demand.
        """
        super().__init__(message)
        self.run_id = run_id
        self.status = status
        self.error_type = error_type
        self.results = tuple(results)
        self._values = values

    def result(self, index: int, result_type: type[Any]) -> Any:
        """Decode one completed Step output by zero-based index.

        Args:
            index: The zero-based position in ``results``.
            result_type: The Python type expected by the caller.

        Returns:
            The decoded Step output.

        Raises:
            IndexError: If ``index`` is outside the available results.
            ValueMappingError: If the output cannot be decoded as ``result_type``.
        """
        value = self.results[index].completed_step_output
        return self._values.decode(value, self._values.codec(result_type))
