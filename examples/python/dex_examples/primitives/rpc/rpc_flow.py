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

"""Minimal RPC Flow: invoke an RPC that unblocks a waiting Step."""

from __future__ import annotations

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    go_to,
    graceful_complete,
    rpc,
)


class RpcWaitStep(Step[int]):
    def __init__(self, internal: Channel[None], second: "RpcCompleteStep") -> None:
        self.internal = internal
        self.second = second

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.until(self.internal.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        return go_to(self.second, 0)


class RpcCompleteStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)


class ExampleStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        return graceful_complete(input)


class RpcFlow(Flow[int]):
    example_ch = Channel[None]("rpc-internal", type(None))
    data = Attribute("rpc-data", str)

    def __init__(self) -> None:
        self.second = RpcCompleteStep()
        self.example_step = ExampleStep()
        self.first = RpcWaitStep(self.example_ch, self.second)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.first).other_steps(self.second, self.example_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.data, self.example_ch)

    @rpc
    def trigger(self, context: Context, input: str) -> RPCResult[str]:
        self.data.set(context, input)
        self.example_ch.publish(context, None)
        return RPCResult(input, next_steps=(StepMovement.of(self.example_step, input),))
