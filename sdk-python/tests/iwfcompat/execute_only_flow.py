# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Context, Flow, Step, StepDecision, StepDef, go_to, graceful_complete


class ExecuteOnlySecondStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)


class ExecuteOnlyFirstStep(Step[int]):
    def __init__(self, second: ExecuteOnlySecondStep) -> None:
        self.second = second

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return go_to(self.second, input + 1)


class ExecuteOnlyFlow(Flow[int]):
    def __init__(self) -> None:
        self.second = ExecuteOnlySecondStep()
        self.first = ExecuteOnlyFirstStep(self.second)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
        )
