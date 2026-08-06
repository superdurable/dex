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
    ExecuteFailure,
    Flow,
    Step,
    StepDecision,
    StepList,
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

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(self.recover)
