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
    BlobCache,
    Client,
    Context,
    Flow,
    Registry,
    RPCResult,
    Step,
    StepList,
    StepDecision,
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


flow: Flow[Input] = TypedFlow()
client = Client(Registry((flow,)), cast(BlobCache, object()))
run_id: str = client.start_flow(flow, "flow-id", Input("input"))
typed_flow = cast(TypedFlow, flow)
output: Output = client.invoke_rpc(
    typed_flow.typed_rpc,
    "flow-id",
    Input("input"),
)
