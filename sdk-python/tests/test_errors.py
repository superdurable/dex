# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from typing import Any, cast

import grpc
from google.protobuf import any_pb2
from google.rpc import status_pb2

from dex import (
    DexServiceError,
    FlowAlreadyStartedError,
    FlowNotActiveError,
    FlowNotFoundError,
    LongPollTimeoutError,
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
from dex.dexpb import dex_pb2 as pb


def test_missing_flow_uses_endpoint_lifecycle_requirement() -> None:
    error = rich_error(grpc.StatusCode.NOT_FOUND, pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS)
    assert isinstance(
        translate_rpc_error(error, "describe_flow", "flow-id", "existing"),
        FlowNotFoundError,
    )
    assert isinstance(
        translate_rpc_error(error, "publish", "flow-id", "active"),
        FlowNotActiveError,
    )


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
