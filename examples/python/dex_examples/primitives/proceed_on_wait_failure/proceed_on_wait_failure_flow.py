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

"""Proceed on WaitFor failure: register PROCEED on StepOptions."""

from __future__ import annotations

from dex import (
    Context,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    WaitForFailurePolicy,
    go_to,
    graceful_complete,
)


class FinishStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(input)


class FailingWaitStep(Step[str]):
    def __init__(self, finish: FinishStep) -> None:
        self.finish = finish

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=2),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
        )

    def wait_for(self, context: Context, input: str) -> Wait:
        raise RuntimeError("planned WaitFor failure")

    def execute(self, context: Context, input: str) -> StepDecision:
        if not context.wait_for_method_failed():
            raise RuntimeError("waitFor failure was not reported")
        return go_to(FinishStep, f"{input}_recovered")


class ProceedOnWaitFailureFlow(Flow[str]):
    def __init__(self) -> None:
        self.finish = FinishStep()
        self.failing_wait = FailingWaitStep(self.finish)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.failing_wait).other_steps(self.finish)
