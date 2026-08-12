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
    StepList,
    StepMovement,
    go_to_multi,
    graceful_complete,
)

class MultiOutputStringStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete("branch-one")

class MultiOutputIntStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete(42)

class MultiOutputStartStep(Step[None]):
    def __init__(
        self,
        string_step: MultiOutputStringStep,
        int_step: MultiOutputIntStep,
    ) -> None:
        self.string_step = string_step
        self.int_step = int_step

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return go_to_multi(
            StepMovement.of(self.string_step, None),
            StepMovement.of(self.int_step, None),
        )

class MultiOutputFlow(Flow[None]):
    def __init__(self) -> None:
        self.string_step = MultiOutputStringStep()
        self.int_step = MultiOutputIntStep()
        self.start = MultiOutputStartStep(self.string_step, self.int_step)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start).other_steps(
            self.string_step,
            self.int_step,
        )
