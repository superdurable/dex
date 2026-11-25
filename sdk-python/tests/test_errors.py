# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from collections.abc import Callable
from types import SimpleNamespace
from typing import Any, cast

import grpc
import pytest
from google.protobuf import any_pb2
from google.rpc import status_pb2

from dex import (
    DexServiceError,
    FlowAlreadyStartedError,
    FlowClosingError,
    FlowNotActiveOrNotFoundError,
    FlowNotFoundError,
    LongPollTimeoutError,
    NonRetryableError,
    RetryAfterError,
    RpcLockConflictError,
    RequestTimeoutError,
    WorkerInvocationError,
    retry_after,
)
from dex._grpc_errors import (
    MAX_WORKER_ERROR_DETAIL_BYTES,
    MAX_WORKER_ERROR_TYPE_BYTES,
    MAX_WORKER_STACK_TRACE_BYTES,
    _worker_error_status,
    translate_rpc_error,
)
from dex._invocation_context import InvocationContext
from dex.dexpb import dex_pb2 as pb


def test_missing_flow_uses_endpoint_lifecycle_requirement() -> None:
    error = rich_error(grpc.StatusCode.NOT_FOUND, pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS)
    assert isinstance(
        translate_rpc_error(error, "describe_flow", "flow-id", "existing"),
        FlowNotFoundError,
    )
    assert isinstance(
        translate_rpc_error(error, "publish", "flow-id", "active"),
        FlowNotActiveOrNotFoundError,
    )


def test_flow_closing_has_a_typed_error() -> None:
    error = rich_error(
        grpc.StatusCode.FAILED_PRECONDITION,
        pb.ERROR_SUB_STATUS_FLOW_CLOSING,
    )
    translated = translate_rpc_error(error, "wait_for_attribute", "flow-id", "active")
    assert isinstance(translated, FlowClosingError)
    assert translated.code is grpc.StatusCode.FAILED_PRECONDITION
    assert translated.sub_status.name == "FLOW_CLOSING"


def test_worker_failure_details_and_lock_conflict_are_distinct() -> None:
    invocation = translate_rpc_error(
        rich_error(
            grpc.StatusCode.FAILED_PRECONDITION,
            pb.ERROR_SUB_STATUS_WORKER_API_ERROR,
            worker_code=grpc.StatusCode.INVALID_ARGUMENT,
            worker_type="ApplicationError",
            worker_detail="invalid order",
        ),
        "invoke_rpc",
        "flow-id",
        "active",
    )
    assert isinstance(invocation, WorkerInvocationError)
    assert invocation.worker_code is grpc.StatusCode.INVALID_ARGUMENT
    assert invocation.worker_error_type == "ApplicationError"
    assert invocation.worker_error_detail == "invalid order"

    conflict = translate_rpc_error(
        rich_error(
            grpc.StatusCode.ABORTED,
            pb.ERROR_SUB_STATUS_WORKER_API_ERROR,
        ),
        "invoke_rpc",
        "flow-id",
        "active",
    )
    assert isinstance(conflict, RpcLockConflictError)


def test_other_known_sub_statuses_have_explicit_errors() -> None:
    started = translate_rpc_error(
        rich_error(
            grpc.StatusCode.ALREADY_EXISTS,
            pb.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED,
        ),
        "start_flow",
        "flow-id",
        "none",
    )
    assert isinstance(started, FlowAlreadyStartedError)

    timeout = translate_rpc_error(
        rich_error(
            grpc.StatusCode.DEADLINE_EXCEEDED,
            pb.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT,
        ),
        "wait_for_flow",
        "flow-id",
        "existing",
    )
    assert isinstance(timeout, LongPollTimeoutError)
    assert timeout.flow_id == "flow-id"

    handler_timeout = translate_rpc_error(
        rich_error(
            grpc.StatusCode.DEADLINE_EXCEEDED,
            pb.ERROR_SUB_STATUS_REQUEST_TIMEOUT,
        ),
        "wait_for_attribute_match",
        "flow-id",
        "active",
    )
    assert isinstance(handler_timeout, RequestTimeoutError)


def test_retry_after_is_available_from_the_sdk_package() -> None:
    cause = RuntimeError("try again")

    result = retry_after(1, cause)

    assert isinstance(result, RetryAfterError)
    assert result.after_seconds == 1
    assert result.cause is cause


@pytest.mark.parametrize(
    "wrap",
    [
        lambda cause: NonRetryableError(cause),
        lambda cause: retry_after(7, NonRetryableError(cause)),
        lambda cause: NonRetryableError(retry_after(7, cause)),
    ],
)
def test_worker_error_status_marks_non_retryable_and_reports_the_cause(
    wrap: Callable[[BaseException], BaseException],
) -> None:
    cause = KeyError("card rejected")

    worker_error = pb.WorkerErrorResponse()
    _worker_error_status(wrap(cause)).details[0].Unpack(worker_error)

    assert worker_error.non_retryable
    assert worker_error.error_type == "builtins.KeyError"
    assert worker_error.detail == str(cause)


class _FailingFinalizer:
    def _finalize_step_output(self) -> None:
        raise RuntimeError("finalizer failed")


def _unpacked_worker_error(failure: BaseException) -> pb.WorkerErrorResponse:
    worker_error = pb.WorkerErrorResponse()
    _worker_error_status(failure).details[0].Unpack(worker_error)
    return worker_error


def test_worker_error_status_finds_the_marker_in_the_sdk_finalizer_group() -> None:
    context = SimpleNamespace(
        _step_outputs_finalized=False,
        _step_output_finalizers=[_FailingFinalizer()],
    )
    marked = retry_after(7, NonRetryableError(KeyError("card rejected")))
    group = InvocationContext._finalize_step_outputs(cast(Any, context), marked)
    assert isinstance(group, BaseExceptionGroup)

    worker_error = _unpacked_worker_error(group)

    assert worker_error.non_retryable
    assert "finalization failed" in worker_error.detail


def test_worker_error_status_finds_the_marker_through_explicit_causes() -> None:
    marked = NonRetryableError(KeyError("card rejected"))
    try:
        raise RuntimeError("charge failed") from marked
    except RuntimeError as wrapper:
        worker_error = _unpacked_worker_error(wrapper)

    assert worker_error.non_retryable
    assert worker_error.error_type == "builtins.RuntimeError"
    assert worker_error.detail == "charge failed"


def test_worker_error_status_keeps_the_outer_retry_after_through_repeated_wrappers() -> (
    None
):
    cause = KeyError("card rejected")
    failure = retry_after(7, NonRetryableError(retry_after(8, cause)))

    worker_error = _unpacked_worker_error(failure)

    assert worker_error.non_retryable
    assert worker_error.retry_after_seconds == 7
    assert worker_error.error_type == "builtins.KeyError"


def test_worker_error_status_stops_on_cause_cycles() -> None:
    first = RuntimeError("first")
    second = RuntimeError("second")
    first.__cause__ = second
    second.__cause__ = first

    assert not _unpacked_worker_error(first).non_retryable


def test_worker_error_status_reports_a_message_non_retryable_error_itself() -> None:
    worker_error = pb.WorkerErrorResponse()
    _worker_error_status(NonRetryableError("bad input")).details[0].Unpack(worker_error)

    assert worker_error.non_retryable
    assert worker_error.error_type == "dex._grpc_errors.NonRetryableError"
    assert worker_error.detail == "bad input"


def test_worker_error_status_bounds_all_text_fields_at_utf8_boundaries() -> None:
    oversized_error = type("世界" * MAX_WORKER_ERROR_TYPE_BYTES, (Exception,), {})
    try:
        raise oversized_error("世界" * MAX_WORKER_STACK_TRACE_BYTES)
    except Exception as failure:
        status = _worker_error_status(failure)
    worker = pb.WorkerErrorResponse()
    assert status.details[0].Unpack(worker)

    assert len(worker.detail.encode()) <= MAX_WORKER_ERROR_DETAIL_BYTES
    assert len(worker.error_type.encode()) <= MAX_WORKER_ERROR_TYPE_BYTES
    assert len(worker.stack_trace.encode()) <= MAX_WORKER_STACK_TRACE_BYTES
    assert worker.detail.endswith("... error detail truncated by Dex Python SDK ...")
    assert worker.error_type.endswith("... error type truncated by Dex Python SDK ...")
    assert worker.stack_trace.endswith(
        "... stack trace truncated by Dex Python SDK ..."
    )
    assert status.message == worker.detail
    assert status.ByteSize() < 7 * 1024
    assert "\ufffd" not in worker.detail
    assert "\ufffd" not in worker.stack_trace


def test_missing_and_malformed_details_use_generic_fallback() -> None:
    missing = FakeRpcError(grpc.StatusCode.INTERNAL, ())
    missing_result = translate_rpc_error(missing, "search_flows", None, "none")
    assert type(missing_result) is DexServiceError

    malformed = FakeRpcError(
        grpc.StatusCode.INTERNAL,
        (("grpc-status-details-bin", b"\xff"),),
    )
    malformed_result = translate_rpc_error(malformed, "search_flows", None, "none")
    assert type(malformed_result) is DexServiceError
    assert "malformed error details" in malformed_result.detail


class FakeRpcError(grpc.RpcError):
    def __init__(
        self,
        code: grpc.StatusCode,
        trailing_metadata: tuple[Any, ...],
    ) -> None:
        super().__init__()
        self._code = code
        self._trailing_metadata = trailing_metadata

    def code(self) -> grpc.StatusCode:
        return self._code

    def details(self) -> str:
        return "gRPC detail"

    def trailing_metadata(self) -> tuple[Any, ...]:
        return self._trailing_metadata


def rich_error(
    code: grpc.StatusCode,
    sub_status: int,
    *,
    worker_code: grpc.StatusCode | None = None,
    worker_type: str = "",
    worker_detail: str = "",
) -> FakeRpcError:
    response = pb.ServiceErrorResponse(
        detail="service detail",
        sub_status=cast(Any, sub_status),
        original_worker_error_status=(worker_code.value[0] if worker_code else 0),
        original_worker_error_type=worker_type,
        original_worker_error_detail=worker_detail,
    )
    packed = any_pb2.Any()
    packed.Pack(response)
    status = status_pb2.Status(
        code=code.value[0],
        message="gRPC detail",
        details=[packed],
    )
    return FakeRpcError(
        code,
        (("grpc-status-details-bin", status.SerializeToString()),),
    )
