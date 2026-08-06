# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

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
