# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepDef,
    StepOptions,
    Wait,
    WaitForFailurePolicy,
    go_to,
)

from .shared import CompleteStringStep


class FailingWaitStep(Step[str]):
    def __init__(self, second: CompleteStringStep) -> None:
        self.second = second

    def wait_for(self, context: Context, input: str) -> Wait:
        del context, input
        raise RuntimeError("wait failure")

    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        return go_to(self.second, input + "-recovered")

    def get_step_options(self) -> StepOptions:
        return StepOptions(wait_for_failure=WaitForFailurePolicy.PROCEED)


class ProceedOnWaitFailureFlow(Flow[str]):
    def __init__(self) -> None:
        self.second = CompleteStringStep()
        self.first = FailingWaitStep(self.second)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.first),
            StepDef.non_start_step(self.second),
        )
