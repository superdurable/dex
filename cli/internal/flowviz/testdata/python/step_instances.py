# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from typing import Generic, TypeVar
from dex import Context, Flow, Step, StepDecision, StepList, StepMovement, StepOptions, StepRef, StepTarget, go_to, go_to_many, graceful_complete

InputT = TypeVar("InputT")


# dex:group group-id:instances group-label:"Instances"
# dex:explanation text:"Route the input to another registered Step instance."
class RoutedStep(Step[InputT], Generic[InputT]):
    def __init__(self, step_type: str, target: StepTarget[InputT], recovery: StepTarget[InputT]) -> None:
        self.step_type = step_type
        self.target = target
        self.recovery = recovery

    def get_step_type(self) -> str:
        return self.step_type

    def get_input_type(self) -> object:
        return str

    def get_step_options(self) -> StepOptions:
        return StepOptions().on_execute_failure_proceed_to(self.recovery)

    def execute(self, context: Context, step_input: InputT) -> StepDecision:
        destination = self.target
        if step_input == "instance":
            return go_to(destination, step_input)
        if step_input == "reference":
            return go_to(StepRef("Second"), step_input)
        if step_input == "movement":
            return go_to_many(StepMovement.of(self.target, step_input), StepMovement.of(StepRef("First"), step_input))
        return graceful_complete().with_canceling_steps(self.target, StepRef("First")).with_canceling_sibling_steps(StepRef("Second"))


# dex:group group-id:instances group-label:"Instances"
# dex:explanation text:"Complete the Flow after a routed Step fails."
class RecoveryStep(Step[str]):
    def get_step_type(self) -> str:
        return "Recovery"

    def execute(self, context: Context, step_input: str) -> StepDecision:
        return graceful_complete()


RECOVERY = RecoveryStep()
FIRST = RoutedStep[str]("First", StepRef("Second"), RECOVERY)


class StepInstancesFlow(Flow[str]):
    second = RoutedStep[str]("Second", FIRST, StepRef("Recovery"))

    def get_steps(self) -> StepList[str]:
        first = FIRST
        return StepList.start_step(first).other_steps(self.second, RECOVERY)
