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
    StepList,
    StepOptions,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)


class MixedTimerStep(Step[int]):
    def __init__(self, options: StepOptions) -> None:
        self.options = options

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.all_of(Timer.by_duration(timedelta(seconds=1)))

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)

    def get_step_options(self) -> StepOptions:
        return self.options


class MixedImmediateStep(Step[int]):
    def __init__(self, second: MixedTimerStep, options: StepOptions) -> None:
        self.second = second
        self.options = options

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return go_to(self.second, input + 1)

    def get_step_options(self) -> StepOptions:
        return self.options


class MixedWaitFlow(Flow[int]):
    def __init__(self) -> None:
        shared = StepOptions(execute_method_timeout=timedelta(seconds=5))
        self.second = MixedTimerStep(shared)
        self.first = MixedImmediateStep(self.second, shared)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.first).other_steps(self.second)
