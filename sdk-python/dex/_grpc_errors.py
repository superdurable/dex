# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

from typing import Any, cast

import grpc
from google.protobuf import any_pb2
from google.rpc import status_pb2
from grpc_status import rpc_status

from dex.dexpb import dex_pb2 as pb
from dex.runtime_errors import DexException, ErrorSubStatus


def translate_rpc_error(error: grpc.RpcError) -> DexException:
    details: pb.ErrorResponse | None = None
    status = rpc_status.from_call(cast(grpc.Call, error))
    if status is not None:
        for packed in status.details:
            candidate = pb.ErrorResponse()
            if packed.Is(candidate.DESCRIPTOR):
                packed.Unpack(candidate)
                details = candidate
                break
    detail = (
        details.detail if details is not None and details.detail else error.details()
    )
    return DexException(
        error.code(),
        _map_sub_status(details.sub_status) if details is not None else None,
        detail,
        details.original_worker_error_type if details is not None else "",
        details.original_worker_error_detail if details is not None else "",
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


def _map_sub_status(value: int) -> ErrorSubStatus | None:
    statuses: dict[int, ErrorSubStatus] = {
        int(pb.ERROR_SUB_STATUS_UNCATEGORIZED): ErrorSubStatus.UNCATEGORIZED,
        int(
            pb.ERROR_SUB_STATUS_FLOW_ALREADY_STARTED
        ): ErrorSubStatus.FLOW_ALREADY_STARTED,
        int(pb.ERROR_SUB_STATUS_FLOW_NOT_EXISTS): ErrorSubStatus.FLOW_NOT_EXISTS,
        int(pb.ERROR_SUB_STATUS_WORKER_API_ERROR): ErrorSubStatus.WORKER_API_ERROR,
        int(pb.ERROR_SUB_STATUS_LONG_POLL_TIME_OUT): ErrorSubStatus.LONG_POLL_TIMEOUT,
    }
    return statuses.get(value)
