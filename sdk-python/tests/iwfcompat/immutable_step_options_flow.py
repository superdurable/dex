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
    go_to,
    go_to_multi,
    graceful_complete,
)


class ImmutableOptionsStartStep(Step[int]):
    def __init__(self, failing_wait: ImmutableOptionsFailingWaitStep) -> None:
        self.failing_wait = failing_wait

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        override = StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=1),
            wait_for_failure=WaitForFailurePolicy.PROCEED,
        )
        return go_to_multi(StepMovement.of(self.failing_wait, 1, options=override))


class ImmutableOptionsFailingWaitStep(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        del context
        raise RuntimeError(f"expected wait failure {input}")

    def execute(self, context: Context, input: int) -> StepDecision:
        if not context.wait_for_method_failed():
            raise RuntimeError("wait failure was not reported")
        if input == 1:
            return go_to(self, 2)
        return graceful_complete(input)

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_retry=RetryPolicy(maximum_attempts=1),
            wait_for_failure=WaitForFailurePolicy.FAIL_FLOW,
        )


class ImmutableStepOptionsFlow(Flow[int]):
    def __init__(self) -> None:
        self.failing_wait = ImmutableOptionsFailingWaitStep()
        self.start = ImmutableOptionsStartStep(self.failing_wait)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(self.failing_wait)
