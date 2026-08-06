# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import (
    Context,
    ExecuteFailure,
    Flow,
    Step,
    StepDecision,
    StepDef,
    StepOptions,
    Wait,
    graceful_complete,
)


class RecoverStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input * 2)


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
        return StepOptions(execute_failure=ExecuteFailure.proceed_to(self.recover))


class StateRecoveryFlow(Flow[int]):
    def __init__(self) -> None:
        self.recover = RecoverStep()
        self.start = FailingStep(self.recover)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.start),
            StepDef.non_start_step(self.recover),
        )
