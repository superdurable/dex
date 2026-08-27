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
    force_fail,
    go_to,
    graceful_complete,
)


class RecoverNoWaitStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        if input == 10:
            return graceful_complete(input)
        if input == 5:
            return go_to(FailingNoWaitStep, input * 2)
        return force_fail(f"unexpected input {input}")


class FailingNoWaitStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("execute failure")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(
                maximum_attempts=1,
                backoff_coefficient=2.0,
            )
        ).on_execute_failure_proceed_to(RecoverNoWaitStep)


class StateRecoveryNoWaitFlow(Flow[int]):
    def __init__(self) -> None:
        self.recover = RecoverNoWaitStep()
        self.start = FailingNoWaitStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(self.recover)
