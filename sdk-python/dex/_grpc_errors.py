# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from __future__ import annotations

import traceback
from dataclasses import dataclass
from typing import Literal, cast

import grpc
from google.protobuf import any_pb2
from google.rpc import status_pb2
from grpc_status import rpc_status

from dex.dexpb import dex_pb2 as pb
from dex.runtime_errors import (
    ChannelMessageNotFoundError,
    DexServiceError,
    ErrorSubStatus,
    FlowAlreadyStartedError,
    FlowClosingError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    LongPollTimeoutError,
    RequestTimeoutError,
    RpcLockConflictError,
    WorkerInvocationError,
)

FlowTargetRequirement = Literal["none", "existing", "active"]

MAX_WORKER_ERROR_DETAIL_BYTES = 1024
MAX_WORKER_ERROR_TYPE_BYTES = 256
MAX_WORKER_STACK_TRACE_BYTES = 4 * 1024
_ERROR_DETAIL_TRUNCATION_MARKER = b"\n... error detail truncated by Dex Python SDK ..."
_ERROR_TYPE_TRUNCATION_MARKER = b"\n... error type truncated by Dex Python SDK ..."
_STACK_TRACE_TRUNCATION_MARKER = b"\n... stack trace truncated by Dex Python SDK ..."


@dataclass(frozen=True)
class RetryAfterError(Exception):
    """Requests a delay before Dex retries a failed worker method.

    Raise an instance with :func:`retry_after` from a Step method when the
    failure is retryable but the next attempt should happen at a known time.
    Dex reports the original cause as the worker failure and uses the requested
    delay instead of the retry policy's calculated interval.

    Attributes:
        after_seconds (int): Positive delay in seconds before the next retry.
        cause (BaseException): Original failure reported to Dex and re-raised
            as the retry cause.
    """

    after_seconds: int
    cause: BaseException

    def __str__(self) -> str:
        return str(self.cause)


def retry_after(after_seconds: int, cause: BaseException) -> RetryAfterError:
    """Creates an error that sets the delay before Dex retries a worker method.

    Raise the returned error from a Step method after an external dependency
    reports that it is not ready. For example::

        raise retry_after(5, RuntimeError("service is not ready"))

    Args:
        after_seconds (int): Positive delay in seconds before the next retry.
        cause (BaseException): Failure that Dex records as the worker error.

    Returns:
        RetryAfterError: Error to raise from the worker method.

    Raises:
        ValueError: If **after_seconds** is not positive or **cause** is None.
    """
    if cause is None:
        raise ValueError("cause is required")
    if after_seconds <= 0:
        raise ValueError("after_seconds must be positive")
    return RetryAfterError(after_seconds=after_seconds, cause=cause)


class NonRetryableError(Exception):
    """Fails the current Step method attempt without further retries.

    Raise it from ``wait_for`` or ``execute`` when a later attempt cannot
    succeed, such as rejected input or a request that a provider reports as
    permanent. Dex skips the remaining attempts of the method's retry policy
    and applies the Step's failure policy at once. By default the Flow fails.
    A ``wait_for`` configured to proceed on failure runs ``execute``, and an
    ``execute`` configured to proceed to a recovery Step starts that Step.

    Pass the original exception to report it as the Worker error, so the
    recovery Step's ``RecoveryErrorInfo`` shows its type, message, and stack
    trace. Pass a message instead to report this error itself. Subclasses are
    also non-retryable.

    It may wrap or be wrapped by a :class:`RetryAfterError`; the non-retryable
    request wins and no retry runs. Raised from an RPC handler, it has no
    effect, because RPC handlers never retry.

    It needs server-zd 0.1.38 or later. An older server ignores the request
    and retries the attempt under the retry policy.

    Example::

        from dex import NonRetryableError, graceful_complete

        class ChargeStep(Step[Order]):
            def execute(self, context: Context, input: Order) -> StepDecision:
                try:
                    receipt = payments.charge(input)
                except CardRejectedError as error:
                    raise NonRetryableError(error) from error
                return graceful_complete(receipt)

    .. versionadded:: 1.45.18

    Attributes:
        cause (BaseException | None): Failure reported to Dex, or None when
            this error was created from a message.
    """

    def __init__(self, cause: BaseException | str) -> None:
        """Creates a non-retryable failure.

        Args:
            cause (BaseException | str): The failure to report to Dex, or a
                message that describes it.

        Raises:
            ValueError: If **cause** is None.
        """
        if cause is None:
            raise ValueError("cause is required")
        if isinstance(cause, BaseException):
            super().__init__(str(cause))
            self.cause: BaseException | None = cause
        else:
            super().__init__(cause)
            self.cause = None


def _unwrap_worker_failure(
    error: BaseException,
) -> tuple[BaseException, RetryAfterError | None, bool]:
    """Return the failure to report, the retry-after request or None, and whether it is non-retryable."""
    reported = error
    retry_after_error: RetryAfterError | None = None
    is_non_retryable = False
    while True:
        if isinstance(reported, RetryAfterError) and retry_after_error is None:
            retry_after_error = reported
            reported = reported.cause
        elif isinstance(reported, NonRetryableError) and not is_non_retryable:
            is_non_retryable = True
            if reported.cause is None:
                return reported, retry_after_error, is_non_retryable
            reported = reported.cause
        else:
            return reported, retry_after_error, is_non_retryable


_HAS_DEX_ERROR_DETAILS = "_has_dex_error_details"


def translate_rpc_error(
    error: grpc.RpcError,
    operation: str,
    flow_id: str | None,
    requirement: FlowTargetRequirement,
) -> DexServiceError:
    translated = _translate_rpc_error(error, operation, flow_id, requirement)
    if _service_error_response(error) is not None:
        setattr(translated, _HAS_DEX_ERROR_DETAILS, True)
    return translated


def has_dex_error_details(error: DexServiceError) -> bool:
    """Return whether the status carried Dex details; only then is its text a read point."""
    return getattr(error, _HAS_DEX_ERROR_DETAILS, False) is True


def _service_error_response(error: grpc.RpcError) -> pb.ServiceErrorResponse | None:
    try:
        status = rpc_status.from_call(cast(grpc.Call, error))
        if status is None:
            return None
        for packed in status.details:
            candidate = pb.ServiceErrorResponse()
            if packed.Is(candidate.DESCRIPTOR):
                packed.Unpack(candidate)
                return candidate
    except Exception:
        return None
    return None


def _translate_rpc_error(
    error: grpc.RpcError,
    operation: str,
    flow_id: str | None,
    requirement: FlowTargetRequirement,
) -> DexServiceError:
    details: pb.ServiceErrorResponse | None = None
    try:
        status = rpc_status.from_call(cast(grpc.Call, error))
        if status is not None:
            for packed in status.details:
                candidate = pb.ServiceErrorResponse()
                if packed.Is(candidate.DESCRIPTOR):
                    packed.Unpack(candidate)
                    details = candidate
                    break
    except Exception as malformed:
        translated = DexServiceError(
            error.code(),
            ErrorSubStatus.UNCATEGORIZED,
            f"Dex returned malformed error details: {malformed}",
            operation,
            flow_id,
        )
        return translated
    detail = (
        details.detail if details is not None and details.detail else error.details()
    ) or str(error)
    code = error.code()
    sub_status = (
        _map_sub_status(details.sub_status)
        if details is not None
        else ErrorSubStatus.UNCATEGORIZED
    )
    parameters = (code, sub_status, detail, operation, flow_id)
    if sub_status is ErrorSubStatus.FLOW_ALREADY_STARTED:
        return FlowAlreadyStartedError(*parameters)
    if sub_status is ErrorSubStatus.FLOW_CLOSING:
        return FlowClosingError(*parameters)
    if sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
        if requirement == "existing":
            return FlowNotFoundError(*parameters)
        if requirement == "active":
            return FlowNotActiveOrNotFoundError(*parameters)
        return DexServiceError(*parameters)
    if sub_status is ErrorSubStatus.WORKER_API_ERROR:
        if code is grpc.StatusCode.ABORTED:
            return RpcLockConflictError(*parameters)
        return WorkerInvocationError(
            *parameters,
            _worker_code(details),
            details.original_worker_error_type if details is not None else "",
            details.original_worker_error_detail if details is not None else "",
        )
    if sub_status is ErrorSubStatus.LONG_POLL_TIMEOUT:
        return LongPollTimeoutError(*parameters)
    if sub_status is ErrorSubStatus.CHANNEL_MESSAGE_NOT_FOUND:
        return ChannelMessageNotFoundError(*parameters)
    if sub_status is ErrorSubStatus.REQUEST_TIMEOUT:
        return RequestTimeoutError(*parameters)
    return DexServiceError(*parameters)


def service_error_texts(error: DexServiceError) -> tuple[str, str]:
    """Return the service detail and the Worker detail, which may be encrypted."""
    worker_detail = (
        error.worker_error_detail if isinstance(error, WorkerInvocationError) else ""
    )
    return error.detail, worker_detail


def with_service_error_texts(
    error: DexServiceError, detail: str, worker_detail: str
) -> DexServiceError:
    """Return ``error`` rebuilt with decrypted texts, or ``error`` when unchanged."""
    if (detail, worker_detail) == service_error_texts(error):
        return error
    if isinstance(error, WorkerInvocationError):
        return WorkerInvocationError(
            error.code,
            error.sub_status,
            detail,
            error.operation,
            error.flow_id,
            error.worker_code,
            error.worker_error_type,
            worker_detail,
        )
    return type(error)(
        error.code, error.sub_status, detail, error.operation, error.flow_id
    )


def translate_clone_fork_flow_error(
    error: grpc.RpcError, source_flow_id: str, flow_id: str
) -> DexServiceError:
    """Attribute not-found to the source Flow and every other error to the new Flow."""
    translated = translate_rpc_error(error, "clone_fork_flow", flow_id, "none")
    if translated.sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
        return translate_rpc_error(error, "clone_fork_flow", source_flow_id, "existing")
    return translated


def _worker_code(details: pb.ServiceErrorResponse | None) -> grpc.StatusCode | None:
    if details is None or details.original_worker_error_status == 0:
        return None
    return next(
        (
            code
            for code in grpc.StatusCode
            if code.value[0] == details.original_worker_error_status
        ),
        grpc.StatusCode.UNKNOWN,
    )


def _worker_error_status(error: BaseException) -> status_pb2.Status:
    reported, retry_after_error, is_non_retryable = _unwrap_worker_failure(error)

    message = _truncate_worker_failure_field(
        str(reported) or type(reported).__name__,
        MAX_WORKER_ERROR_DETAIL_BYTES,
        _ERROR_DETAIL_TRUNCATION_MARKER,
    )
    stack_trace_source = reported
    if retry_after_error is not None or reported.__traceback__ is None:
        stack_trace_source = error
    worker_error = pb.WorkerErrorResponse(
        detail=message,
        error_type=_truncate_worker_failure_field(
            f"{type(reported).__module__}.{type(reported).__qualname__}",
            MAX_WORKER_ERROR_TYPE_BYTES,
            _ERROR_TYPE_TRUNCATION_MARKER,
        ),
        stack_trace=_worker_stack_trace(stack_trace_source),
        grpc_status=grpc.StatusCode.UNKNOWN.value[0],
        non_retryable=is_non_retryable,
    )
    if retry_after_error is not None:
        worker_error.retry_after_seconds = retry_after_error.after_seconds
    packed = any_pb2.Any()
    packed.Pack(worker_error)
    return status_pb2.Status(
        code=grpc.StatusCode.UNKNOWN.value[0],
        message=message,
        details=[packed],
    )


def _worker_stack_trace(error: BaseException) -> str:
    if error.__traceback__ is None:
        return ""
    lines = traceback.format_exception(type(error), error, error.__traceback__)
    return _truncate_worker_failure_field(
        "".join(lines),
        MAX_WORKER_STACK_TRACE_BYTES,
        _STACK_TRACE_TRUNCATION_MARKER,
    )


def _truncate_worker_failure_field(
    value: str,
    maximum_bytes: int,
    truncation_marker: bytes,
) -> str:
    encoded = value.encode()
    if len(encoded) <= maximum_bytes:
        return value
    prefix_length = maximum_bytes - len(truncation_marker)
    while prefix_length > 0 and (encoded[prefix_length] & 0xC0) == 0x80:
        prefix_length -= 1
    return encoded[:prefix_length].decode() + truncation_marker.decode()


def _map_sub_status(value: int) -> ErrorSubStatus:
    statuses: dict[int, ErrorSubStatus] = {
        int(pb.ERROR_SUB_STATUS_UNCATEGORIZED): ErrorSubStatus.UNCATEGORIZED,
        int(
            pb.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED
        ): ErrorSubStatus.FLOW_ALREADY_STARTED,
        int(pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS): ErrorSubStatus.FLOW_NOT_EXISTS,
        int(pb.ERROR_SUB_STATUS_WORKER_API_ERROR): ErrorSubStatus.WORKER_API_ERROR,
        int(pb.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT): ErrorSubStatus.LONG_POLL_TIMEOUT,
        int(
            pb.ERROR_SUB_STATUS_CHANNEL_MESSAGE_NOT_FOUND
        ): ErrorSubStatus.CHANNEL_MESSAGE_NOT_FOUND,
        int(pb.ERROR_SUB_STATUS_REQUEST_TIMEOUT): ErrorSubStatus.REQUEST_TIMEOUT,
        int(pb.ERROR_SUB_STATUS_FLOW_CLOSING): ErrorSubStatus.FLOW_CLOSING,
    }
    return statuses.get(value, ErrorSubStatus.UNCATEGORIZED)
