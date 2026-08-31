# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from __future__ import annotations

import logging
from asyncio import CancelledError, current_task
from collections.abc import AsyncIterator, Awaitable, Callable
from typing import Any, TypeVar

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

    async def InvokeWaitForMethod(
        self,
        request: pb.InvokeWaitForMethodRequest,
        context: grpc.aio.ServicerContext[
            pb.InvokeWaitForMethodRequest,
            pb.InvokeWaitForMethodOutput,
        ],
    ) -> AsyncIterator[pb.InvokeWaitForMethodOutput]:
        async for output in self._invoke_stream(
            context,
            lambda: self._dispatcher.invoke_wait_for(
                request, lambda: self._is_active(context)
            ),
        ):
            yield output

    async def InvokeExecuteMethod(
        self,
        request: pb.InvokeExecuteMethodRequest,
        context: grpc.aio.ServicerContext[
            pb.InvokeExecuteMethodRequest,
            pb.InvokeExecuteMethodOutput,
        ],
    ) -> AsyncIterator[pb.InvokeExecuteMethodOutput]:
        async for output in self._invoke_stream(
            context,
            lambda: self._dispatcher.invoke_execute(
                request, lambda: self._is_active(context)
            ),
        ):
            yield output

    async def InvokeWorkerRPC(
        self,
        request: pb.InvokeWorkerRPCRequest,
        context: grpc.aio.ServicerContext[
            pb.InvokeWorkerRPCRequest,
            pb.InvokeWorkerRPCResponse,
        ],
    ) -> pb.InvokeWorkerRPCResponse:
        return await self._invoke(
            context,
            lambda: self._dispatcher.invoke_rpc(
                request, lambda: self._is_active(context)
            ),
        )

    @staticmethod
    def _is_active(context: grpc.aio.ServicerContext[Any, Any]) -> bool:
        task = current_task()
        return not context.cancelled() and (task is None or task.cancelling() == 0)

    @staticmethod
    async def _invoke(
        context: grpc.aio.ServicerContext[Any, Any],
        invocation: Callable[[], Awaitable[ResponseT]],
    ) -> ResponseT:
        try:
            return await invocation()
        except CancelledError:
            raise
        except BaseException as error:
            if not AsyncWorkerService._is_active(context):
                await context.abort(
                    grpc.StatusCode.CANCELLED,
                    "Python AsyncWorker invocation canceled",
                )
            _LOGGER.exception("Python AsyncWorker invocation failed")
            await async_abort_worker_error(context, error)
            raise RuntimeError("gRPC abort returned unexpectedly") from error

    @staticmethod
    async def _invoke_stream(
        context: grpc.aio.ServicerContext[Any, Any],
        invocation: Callable[[], AsyncIterator[ResponseT]],
    ) -> AsyncIterator[ResponseT]:
        try:
            async for response in invocation():
                yield response
        except GeneratorExit:
            raise
        except CancelledError:
            raise
        except BaseException as error:
            if not AsyncWorkerService._is_active(context):
                await context.abort(
                    grpc.StatusCode.CANCELLED,
                    "Python AsyncWorker invocation canceled",
                )
            _LOGGER.exception("Python AsyncWorker invocation failed")
            await async_abort_worker_error(context, error)
            raise RuntimeError("gRPC abort returned unexpectedly") from error
