# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

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
    go_to_multi,
    graceful_complete,
)


class OverrideFirstStep(Step[str]):
    def __init__(self, second: OverrideCompleteStep) -> None:
        self.second = second
        self.output = ""

    def wait_for(self, context: Context, input: str) -> Wait:
        del context
        self.output = input + "_state1_start"
        return Wait.skip_immediately()

    def execute(self, context: Context, input: str) -> StepDecision:
        del context
        self.output += "_state1_decide"
        options = StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=2),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
        )
        return go_to_multi(StepMovement.of(self.second, self.output, options=options))


class OverrideCompleteStep(Step[str]):
    def __init__(self) -> None:
        self.output = ""

    def wait_for(self, context: Context, input: str) -> Wait:
        del context
        self.output = input + "_state2_start"
        raise RuntimeError("state 2 wait failure")

    def execute(self, context: Context, input: str) -> StepDecision:
        del input
        if not context.wait_for_method_failed():
            raise RuntimeError("wait_for failure was not reported")
        self.output += "_state2_decide"
        return graceful_complete(self.output)

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=1),
            wait_for_failure=WaitForFailurePolicy.FAIL_FLOW,
        )


class StateOptionsOverrideFlow(Flow[str]):
    def __init__(self) -> None:
        self.second = OverrideCompleteStep()
        self.first = OverrideFirstStep(self.second)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.first).other_steps(self.second)
