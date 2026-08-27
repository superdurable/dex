# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from dex import Context, Flow, Step, StepDecision, StepList, go_to, graceful_complete


class ExecuteOnlySecondStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input + 1)


class ExecuteOnlyFirstStep(Step[int]):
    def __init__(self, second: ExecuteOnlySecondStep) -> None:
        self.second = second

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return go_to(ExecuteOnlySecondStep, input + 1)


class ExecuteOnlyFlow(Flow[int]):
    def __init__(self) -> None:
        self.second = ExecuteOnlySecondStep()
        self.first = ExecuteOnlyFirstStep(self.second)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.first).other_steps(self.second)
