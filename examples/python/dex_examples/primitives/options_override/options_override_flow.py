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

from dex import (
    Context,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    Wait,
    WaitForFailurePolicy,
    go_to_many,
    graceful_complete,
)


class OverrideFirstStep(Step[str]):
    def __init__(self, second: OverrideSecondStep) -> None:
        self.second = second

    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        override = StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=2),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
        )
        payload = f"{input}_state1"
        return go_to_many(StepMovement.of(OverrideSecondStep, payload, options=override))


class OverrideSecondStep(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        raise RuntimeError("state 2 wait failure")

    def execute(self, context: Context, input: str) -> StepDecision:
        if not context.wait_for_method_failed():
            raise RuntimeError("waitFor failure was not reported")
        return graceful_complete(f"{input}_state2")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=1),
            wait_for_failure=WaitForFailurePolicy.FAIL_FLOW,
        )


class OptionsOverrideFlow(Flow[str]):
    def __init__(self) -> None:
        self.second = OverrideSecondStep()
        self.first = OverrideFirstStep(self.second)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.first).other_steps(self.second)
