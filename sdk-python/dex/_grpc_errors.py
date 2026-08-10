# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from typing import Any, Literal, cast

import grpc
from google.protobuf import any_pb2
from google.rpc import status_pb2
from grpc_status import rpc_status

from dex.dexpb import dex_pb2 as pb
from dex.runtime_errors import (
    DexServiceError,
    ErrorSubStatus,
    FlowAlreadyStartedError,
    FlowNotActiveError,
    FlowNotFoundError,
    LongPollTimeoutError,
    RpcLockConflictError,
    WorkerInvocationError,
)

FlowTargetRequirement = Literal["none", "existing", "active"]


def translate_rpc_error(
    error: grpc.RpcError,
    operation: str,
    flow_id: str | None,
    requirement: FlowTargetRequirement,
) -> DexServiceError:
    details: pb.ErrorResponse | None = None
    try:
        status = rpc_status.from_call(cast(grpc.Call, error))
        if status is not None:
            for packed in status.details:
                candidate = pb.ErrorResponse()
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
    if sub_status is ErrorSubStatus.FLOW_NOT_EXISTS:
        if requirement == "existing":
            return FlowNotFoundError(*parameters)
        if requirement == "active":
            return FlowNotActiveError(*parameters)
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
    return DexServiceError(*parameters)


def _worker_code(details: pb.ErrorResponse | None) -> grpc.StatusCode | None:
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


def abort_worker_error(
    context: grpc.ServicerContext,
    error: BaseException,
) -> None:
    context.abort_with_status(rpc_status.to_status(_worker_error_status(error)))


async def async_abort_worker_error(
    context: grpc.aio.ServicerContext[Any, Any],
    error: BaseException,
) -> None:
    # types-grpcio on 3.11 omits aio abort_with_status; runtime provides it.
    await cast(Any, context).abort_with_status(
        rpc_status.to_status(_worker_error_status(error))
    )


def _worker_error_status(error: BaseException) -> status_pb2.Status:
    message = str(error) or type(error).__name__
    worker_error = pb.WorkerErrorResponse(
        detail=message,
        error_type=f"{type(error).__module__}.{type(error).__qualname__}",
    )
    packed = any_pb2.Any()
    packed.Pack(worker_error)
    return status_pb2.Status(
        code=grpc.StatusCode.UNKNOWN.value[0],
        message=message,
        details=[packed],
    )


def _map_sub_status(value: int) -> ErrorSubStatus:
    statuses: dict[int, ErrorSubStatus] = {
        int(pb.ERROR_SUB_STATUS_UNCATEGORIZED): ErrorSubStatus.UNCATEGORIZED,
        int(
            pb.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED
        ): ErrorSubStatus.FLOW_ALREADY_STARTED,
        int(pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS): ErrorSubStatus.FLOW_NOT_EXISTS,
        int(pb.ERROR_SUB_STATUS_WORKER_API_ERROR): ErrorSubStatus.WORKER_API_ERROR,
        int(pb.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT): ErrorSubStatus.LONG_POLL_TIMEOUT,
    }
    return statuses.get(value, ErrorSubStatus.UNCATEGORIZED)
