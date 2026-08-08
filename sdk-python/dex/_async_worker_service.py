# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import logging
from collections.abc import Awaitable, Callable
from typing import TypeVar

import grpc

from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex._grpc_errors import async_abort_worker_error
from dex.dexpb import dex_pb2 as pb
from dex.dexpb import dex_pb2_grpc

_LOGGER = logging.getLogger(__name__)
ResponseT = TypeVar("ResponseT")


class AsyncWorkerService(dex_pb2_grpc.WorkerServiceServicer):
    def __init__(self, dispatcher: AsyncWorkerDispatcher) -> None:
        self._dispatcher = dispatcher

    async def InvokeWaitForMethod(  # type: ignore[override]
        self,
        request: pb.InvokeWaitForMethodRequest,
        context: grpc.aio.ServicerContext,
    ) -> pb.InvokeWaitForMethodResponse:
        return await self._invoke(
            context, lambda: self._dispatcher.invoke_wait_for(request)
        )

    async def InvokeExecuteMethod(  # type: ignore[override]
        self,
        request: pb.InvokeExecuteMethodRequest,
        context: grpc.aio.ServicerContext,
    ) -> pb.InvokeExecuteMethodResponse:
        return await self._invoke(
            context, lambda: self._dispatcher.invoke_execute(request)
        )

    async def InvokeWorkerRPC(  # type: ignore[override]
        self,
        request: pb.InvokeWorkerRPCRequest,
        context: grpc.aio.ServicerContext,
    ) -> pb.InvokeWorkerRPCResponse:
        return await self._invoke(context, lambda: self._dispatcher.invoke_rpc(request))

    @staticmethod
    async def _invoke(
        context: grpc.aio.ServicerContext,
        invocation: Callable[[], Awaitable[ResponseT]],
    ) -> ResponseT:
        try:
            return await invocation()
        except BaseException as error:
            _LOGGER.exception("Python AsyncWorker invocation failed")
            await async_abort_worker_error(context, error)
            raise RuntimeError("gRPC abort returned unexpectedly") from error
