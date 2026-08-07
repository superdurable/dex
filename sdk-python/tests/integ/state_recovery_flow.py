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
    StepOptions,
    Wait,
    force_fail,
    go_to,
    graceful_complete,
)


class RecoverStep(Step[int]):
    def __init__(self) -> None:
        self.failing: FailingStep | None = None

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.skip_immediately()

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        if input == 10:
            return graceful_complete(input)
        if input == 5 and self.failing is not None:
            return go_to(self.failing, input * 2)
        return force_fail(f"unexpected input {input}")


class FailingStep(Step[int]):
    def __init__(self, recover: RecoverStep) -> None:
        self.recover = recover

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.skip_immediately()

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("execute failure")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                maximum_attempts=1,
                backoff_coefficient=2.0,
            )
        ).on_execute_failure_proceed_to(self.recover)


class StateRecoveryFlow(Flow[int]):
    def __init__(self) -> None:
        self.recover = RecoverStep()
        self.start = FailingStep(self.recover)
        self.recover.failing = self.start

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(self.recover)
