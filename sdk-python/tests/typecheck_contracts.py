# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from typing import cast

from dex import (
    Attribute,
    AttributeMap,
    BlobCache,
    Client,
    Context,
    Flow,
    PersistenceSchema,
    Registry,
    RPCResult,
    StartFlowOptions,
    Step,
    StepList,
    StepDecision,
    Wait,
    Worker,
    graceful_complete,
    rpc,
)


@dataclass(frozen=True)
class Input:
    value: str


@dataclass(frozen=True)
class Output:
    value: int


class TypedStep(Step[Input]):
    def execute(self, context: Context, input: Input) -> StepDecision:
        del context, input
        return graceful_complete()


class TypedFlow(Flow[Input]):
    start = TypedStep()

    def get_steps(self) -> StepList[Input]:
        return StepList.start_step(self.start)

    @rpc()
    def typed_rpc(self, context: Context, input: Input) -> RPCResult[Output]:
        del context, input
        return RPCResult(Output(1))


class AsyncTypedStep(Step[Input]):
    async def wait_for(self, context: Context, input: Input) -> Wait:
        del context, input
        return Wait.skip_immediately()

    async def execute(self, context: Context, input: Input) -> StepDecision:
        del context, input
        return graceful_complete()


class AsyncTypedFlow(Flow[Input]):
    start = AsyncTypedStep()

    def get_steps(self) -> StepList[Input]:
        return StepList.start_step(self.start)

    @rpc()
    async def typed_rpc(
        self,
        context: Context,
        input: Input,
    ) -> RPCResult[Output]:
        del context, input
        return RPCResult(Output(1))


flow: Flow[Input] = TypedFlow()
client = Client(Registry((flow,)), cast(BlobCache, object()))
run_id: str = client.start_flow(flow, "flow-id", Input("input"))
typed_flow = cast(TypedFlow, flow)
output: Output = client.invoke_rpc(
    typed_flow.typed_rpc,
    "flow-id",
    Input("input"),
)

async_flow: Flow[Input] = AsyncTypedFlow()
async_client = Client(Registry((async_flow,)), cast(BlobCache, object()))
typed_async_flow = cast(AsyncTypedFlow, async_flow)
async_output: Output = async_client.invoke_rpc(
    typed_async_flow.typed_rpc,
    "async-flow-id",
    Input("input"),
)

status = Attribute("status", str)
items = AttributeMap("items", int)
persistence: PersistenceSchema = PersistenceSchema.of(status, items)
start_options: StartFlowOptions = (
    StartFlowOptions()
    .with_attribute(status, "ready")
    .with_attribute(items, "order-1", 1)
)


def compile_lifecycle(client: Client, worker: Worker) -> None:
    client.trigger_continue_as_new("flow-id")
    with client:
        pass
    with worker:
        pass
