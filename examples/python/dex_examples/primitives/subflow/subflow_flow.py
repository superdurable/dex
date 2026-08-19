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

"""Minimal SubFlow: parent Step waits for a child Flow to complete."""

from __future__ import annotations

from datetime import timedelta

from dex import (
    Context,
    Flow,
    FlowTimeoutPolicy,
    Step,
    StepDecision,
    StepList,
    SubFlow,
    SubFlowOptions,
    Wait,
    graceful_complete,
)


class SubFlowChildStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)


class SubFlowChildFlow(Flow[int]):
    def __init__(self) -> None:
        self.start = SubFlowChildStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)


class SubFlowParentStep(Step[int]):
    def __init__(self, target: Flow[int]) -> None:
        self.target = target
        self.options = SubFlowOptions(
            timeout=timedelta(hours=1),
            timeout_policy=FlowTimeoutPolicy.CANCEL,
        )

    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.until(SubFlow.run(self.target, input, self.options))

    def execute(self, context: Context, input: int) -> StepDecision:
        del input
        result = SubFlow.get_condition_results(context)
        output = result.single_output(int)
        return graceful_complete(f"{SubFlow.get_flow_id(context)}|{output}")


class SubFlowParentFlow(Flow[int]):
    def __init__(self, target: Flow[int]) -> None:
        self.start = SubFlowParentStep(target)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
