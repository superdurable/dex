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
