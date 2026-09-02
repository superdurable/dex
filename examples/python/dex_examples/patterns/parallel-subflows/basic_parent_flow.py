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

from dex import Context, Flow, Step, StepDecision, StepList, SubFlow, Wait, graceful_complete

from dex_examples.patterns.parallel_subflows.example_subflow import ExampleSubFlow


class SubFlowsStep(Step[list[str]]):
    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.example_subflow = example_subflow

    def wait_for(self, context: Context, requests: list[str]) -> Wait:
        return Wait.all_of(
            *(SubFlow.run(self.example_subflow, request) for request in requests)
        )

    def execute(self, context: Context, requests: list[str]) -> StepDecision:
        return graceful_complete()


class BasicParentFlow(Flow[list[str]]):
    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.subflows = SubFlowsStep(example_subflow)

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.subflows)
