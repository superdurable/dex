# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepDef,
    Wait,
    go_to,
    graceful_complete,
)


class BasicSecondStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)


class BasicFirstStep(Step[int]):
    def __init__(self, second: BasicSecondStep) -> None:
        self.second = second

    def wait_for(self, context: Context, input: int) -> Wait:
        context.set_step_execution_local("input", input)
        return Wait.skip_immediately()

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return go_to(self.second, input + 1)


class BasicFlow(Flow[int]):
    def __init__(self) -> None:
        self.second = BasicSecondStep()
        self.first = BasicFirstStep(self.second)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
        )
