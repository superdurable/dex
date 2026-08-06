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
    Timer,
    Wait,
    graceful_complete,
)


class TimerStep(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        return Wait.all_of(
            Timer.by_duration(
                timedelta(seconds=input),
                condition_id="test-timer-id",
            )
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        return graceful_complete()


class TimerFlow(Flow[int]):
    start = TimerStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)
