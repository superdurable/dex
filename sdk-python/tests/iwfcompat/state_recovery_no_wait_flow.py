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
    graceful_complete,
)


class RecoverNoWaitStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input * 2)


class FailingNoWaitStep(Step[int]):
    def __init__(self, recover: RecoverNoWaitStep) -> None:
        self.recover = recover

    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("execute failure")

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_failure=ExecuteFailure.proceed_to(self.recover))


class StateRecoveryNoWaitFlow(Flow[int]):
    def __init__(self) -> None:
        self.recover = RecoverNoWaitStep()
        self.start = FailingNoWaitStep(self.recover)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.start),
            StepDef.non_start_step(self.recover),
        )
