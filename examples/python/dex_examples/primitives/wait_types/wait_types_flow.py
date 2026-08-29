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

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta

from dex import (
    Channel,
    ConditionCombination,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    graceful_complete,
    rpc,
)

channel_a = Channel("SignalA", str)
channel_b = Channel("SignalB", str)


@dataclass(frozen=True)
class WaitTypesInput:
    mode: str
    timeout_seconds: int


class WaitTypesStep(Step[WaitTypesInput]):
    def wait_for(self, context: Context, input: WaitTypesInput) -> Wait:
        timeout = timedelta(seconds=input.timeout_seconds)
        if input.mode == "any":
            return Wait.any_of(
                channel_a.for_one(condition_id="signal"),
                Timer.by_duration(timeout, condition_id="timeout"),
            )
        if input.mode == "all":
            return Wait.all_of(
                channel_a.for_one(condition_id="signal-a"),
                channel_b.for_one(condition_id="signal-b"),
            )
        if input.mode == "combo":
            return Wait.any_combination_of(
                ConditionCombination.of(
                    channel_a.for_one(condition_id="signal-a"),
                    Timer.by_duration(timeout, condition_id="timeout"),
                ),
                ConditionCombination.of(
                    channel_b.for_one(condition_id="signal-b"),
                ),
            )
        raise ValueError(f"unknown wait mode {input.mode!r}")

    def execute(self, context: Context, input: WaitTypesInput) -> StepDecision:
        return graceful_complete(input.mode)


class WaitTypesFlow(Flow[WaitTypesInput]):
    def __init__(self) -> None:
        self.wait_types = WaitTypesStep()

    def get_steps(self) -> StepList[WaitTypesInput]:
        return StepList.start_step(self.wait_types)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(channel_a, channel_b)

    @rpc
    def signal_a(self, context: Context) -> None:
        channel_a.publish(context, "signal-a")

    @rpc
    def signal_b(self, context: Context) -> None:
        channel_b.publish(context, "signal-b")
