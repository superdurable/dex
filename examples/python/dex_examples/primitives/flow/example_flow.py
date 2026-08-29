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
# See the License for the applicable language governing permissions and
# limitations under the License.

"""Minimal Flow sample: Steps, persistence schema, and an RPC."""

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
    Wait,
    go_to,
    force_fail,
    graceful_complete,
    rpc,
)


class FinishStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        status.set(context, "done")
        return graceful_complete(input + 1)


class ExampleStep(Step[int]):
    def __init__(self, finish: FinishStep) -> None:
        self.finish = finish

    def wait_for(self, context: Context, input: int) -> Wait:
        status.set(context, "running")
        return Wait.skip_immediately()

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to(FinishStep, input + 1)


status = Attribute("status", str)
notify = Channel("notify", None)


class ExampleFlow(Flow[int]):
    def __init__(self) -> None:
        self.finish = FinishStep()
        self.example = ExampleStep(self.finish)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.example).other_steps(self.finish)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(status, notify)

    def handle_timeout(self, context: Context) -> StepDecision:
        status.set(context, "timed out")
        return force_fail("processing deadline reached")

    @rpc
    def describe(self, context: Context) -> RPCResult[str]:
        return RPCResult.of(status.get(context))
