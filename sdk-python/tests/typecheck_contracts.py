# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dataclasses import dataclass
from typing import Generator, cast

from dex import (
    AsyncContext,
    Attribute,
    AttributeMap,
    BlobCache,
    Client,
    Context,
    Flow,
    FlowConfig,
    PersistenceSchema,
    Registry,
    RPCResult,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    StepOutput,
    Stream,
    StreamMessage,
    Worker,
    graceful_complete,
    heartbeat,
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


class StreamingStep(Step[Input]):
    def execute(
        self,
        context: Context,
        input: Input,
    ) -> Generator[StepOutput, None, StepDecision]:
        yield heartbeat({"input": input.value})
        yield progress.write(context, "working")
        return graceful_complete()


class AsyncTypedStep(Step[Input]):
    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: Input,
    ) -> StepDecision:
        progress.write(context, input.value)
        await context.heartbeat(input.value)
        return graceful_complete()


progress = Stream("progress", str, 10 * 1024 * 1024)


class TypedFlow(Flow[Input]):
    start = TypedStep()

    def get_steps(self) -> StepList[Input]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(progress)

    @rpc()
    def typed_rpc(self, context: Context, input: Input) -> RPCResult[Output]:
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
client.write_stream("flow-id", progress, "frontend/1", "starting")
stream_message: StreamMessage[str] = client.read_stream("flow-id", progress)
stream_source: str = stream_message.source

status = Attribute("status", str, sync_to_attribute_store=True)
items = AttributeMap("items", int, sync_to_attribute_store=True)
persistence: PersistenceSchema = PersistenceSchema.of(status, items)
start_options: StartFlowOptions = (
    StartFlowOptions(
        config_override=FlowConfig(attribute_store_names=["reporting", "audit"])
    )
    .with_attribute(status, "ready")
    .with_attribute(items, "order-1", 1)
)


def compile_lifecycle(client: Client, worker: Worker) -> None:
    client.trigger_continue_as_new("flow-id")
    with client:
        pass
    with worker:
        pass
