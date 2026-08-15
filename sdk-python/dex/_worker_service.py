# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import logging
from typing import Callable, TypeVar

import grpc

from dex._grpc_errors import abort_worker_error
from dex._worker_dispatcher import WorkerDispatcher
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc

_LOGGER = logging.getLogger(__name__)
ResponseT = TypeVar("ResponseT")


class WorkerService(dex_pb2_grpc.WorkerServiceServicer):
    def __init__(self, dispatcher: WorkerDispatcher) -> None:
        self._dispatcher = dispatcher

    def InvokeWaitForMethod(
        self,
        request: pb.InvokeWaitForMethodRequest,
        context: grpc.ServicerContext,
    ) -> pb.InvokeWaitForMethodResponse:
        return self._invoke(
            context,
            lambda: self._dispatcher.invoke_wait_for(request, context.is_active),
        )

    def InvokeExecuteMethod(
        self,
        request: pb.InvokeExecuteMethodRequest,
        context: grpc.ServicerContext,
    ) -> pb.InvokeExecuteMethodResponse:
        return self._invoke(
            context,
            lambda: self._dispatcher.invoke_execute(request, context.is_active),
        )

    def InvokeWorkerRPC(
        self,
        request: pb.InvokeWorkerRPCRequest,
        context: grpc.ServicerContext,
    ) -> pb.InvokeWorkerRPCResponse:
        return self._invoke(
            context,
            lambda: self._dispatcher.invoke_rpc(request, context.is_active),
        )

    @staticmethod
    def _invoke(
        context: grpc.ServicerContext,
        invocation: Callable[[], ResponseT],
    ) -> ResponseT:
        try:
            return invocation()
        except BaseException as error:
            if not context.is_active():
                context.abort(
                    grpc.StatusCode.CANCELLED,
                    "Python Worker invocation canceled",
                )
            _LOGGER.exception("Python Worker invocation failed")
            abort_worker_error(context, error)
            raise RuntimeError("gRPC abort returned unexpectedly") from error
