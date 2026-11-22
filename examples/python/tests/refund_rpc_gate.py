# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import asyncio
from threading import Event
from typing import Callable, TypeVar

from dex import Attribute, Context
from dex._async_worker_dispatcher import AsyncWorkerDispatcher
from dex.dexpb.dex_pb2 import InvokeWorkerRPCRequest, InvokeWorkerRPCResponse
from dex_examples.products.customer_refund.refund_flow import CASE_STATUS

ValueT = TypeVar("ValueT")


class RefundRpcGate:
    def __init__(self, flow_id: str) -> None:
        self.flow_id = flow_id
        self.lock_acquired = Event()
        self.release_lock = Event()
        self.read_attribute = Attribute.get

    def hold_lock(self, attribute: Attribute[ValueT], context: Context) -> ValueT:
        if attribute is CASE_STATUS and context.flow_id == self.flow_id:
            self.lock_acquired.set()
            assert self.release_lock.wait(10), "test did not release the approval RPC"
        return self.read_attribute(attribute, context)


class AsyncRefundRpcGate:
    def __init__(self, flow_id: str) -> None:
        self.flow_id = flow_id
        self.lock_acquired = asyncio.Event()
        self.release_lock = asyncio.Event()
        self.invoke_rpc = AsyncWorkerDispatcher.invoke_rpc

    async def hold_lock(
        self,
        dispatcher: AsyncWorkerDispatcher,
        request: InvokeWorkerRPCRequest,
        is_active: Callable[[], bool] | None,
    ) -> InvokeWorkerRPCResponse:
        if request.context.flow_id == self.flow_id:
            self.lock_acquired.set()
            await asyncio.wait_for(self.release_lock.wait(), timeout=10)
        return await self.invoke_rpc(dispatcher, request, is_active)
