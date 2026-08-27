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

from __future__ import annotations

from datetime import timedelta

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepOptions,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)


class RouteDurabilityStep(Step[str]):
    def __init__(self, flow: DurabilityFlow) -> None:
        self.flow = flow

    def execute(self, context: Context, mode: str) -> StepDecision:
        del context
        if mode == "async":
            return go_to(AsyncWorkStep, mode)
        return go_to(SyncWorkStep, mode)


class SyncWorkStep(Step[str]):
    def __init__(self, flow: DurabilityFlow) -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_durability=StepDurability.SYNC)

    def execute(self, context: Context, mode: str) -> StepDecision:
        del context
        return go_to(FinishDurabilityStep, f"sync:{mode}")


class AsyncWorkStep(Step[str]):
    def __init__(self, flow: DurabilityFlow) -> None:
        self.flow = flow

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_durability=StepDurability.ASYNC)

    def execute(self, context: Context, mode: str) -> StepDecision:
        del context
        return go_to(FinishDurabilityStep, f"async:{mode}")


class FinishDurabilityStep(Step[str]):
    def wait_for(self, context: Context, label: str) -> Wait:
        del context, label
        return Wait.until(Timer.by_duration(timedelta(seconds=1)))

    def execute(self, context: Context, label: str) -> StepDecision:
        del context
        return graceful_complete(label)


class DurabilityFlow(Flow[str]):
    def __init__(self) -> None:
        self.finish = FinishDurabilityStep()
        self.sync_work = SyncWorkStep(self)
        self.async_work = AsyncWorkStep(self)
        self.route = RouteDurabilityStep(self)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.route).other_steps(
            self.sync_work,
            self.async_work,
            self.finish,
        )
