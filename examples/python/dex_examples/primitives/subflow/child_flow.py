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

from dex import Context, Flow, Step, StepDecision, StepList, graceful_complete


class SubFlowChildStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input + 1)


class SubFlowChildFlow(Flow[int]):
    def __init__(self) -> None:
        self.start = SubFlowChildStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
