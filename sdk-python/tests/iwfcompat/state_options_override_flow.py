# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import timedelta

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepDef,
    StepMovement,
    StepOptions,
    go_to_multi,
)

from .shared import CompleteStringStep


class OverrideFirstStep(Step[str]):
    def __init__(self, second: CompleteStringStep) -> None:
        self.second = second

    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        options = StepOptions(
            wait_for_method_timeout=timedelta(seconds=2),
            execute_method_timeout=timedelta(seconds=3),
        )
        return go_to_multi(StepMovement.of(self.second, input, options=options))


class StateOptionsOverrideFlow(Flow[str]):
    def __init__(self) -> None:
        self.second = CompleteStringStep()
        self.first = OverrideFirstStep(self.second)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
        )
