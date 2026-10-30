# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

from typing import Generic, TypeVar
from dex import Flow, Step, StepDecision, StepList, StepMovement, StepOptions, StepRef, go_to, go_to_many, graceful_complete

InputT = TypeVar("InputT")


# dex:group group-id:instances group-label:"Instances"
# dex:explanation text:"Route the input to another registered Step instance."
class RoutedStep(Step[InputT], Generic[InputT]):
    def __init__(self, step_type, target):
        self.step_type = step_type
        self.target = target

    def get_step_type(self):
        return self.step_type

    def get_step_options(self):
        return StepOptions().on_execute_failure_proceed_to(self.target)

    def execute(self, context, step_input) -> StepDecision:
        destination = self.target
        if step_input == "instance":
            return go_to(destination, step_input)
        if step_input == "reference":
            return go_to(StepRef("Second"), step_input)
        if step_input == "inline":
            return go_to(RoutedStep[str]("Second", StepRef("First")), step_input)
        if step_input == "movement":
            return go_to_many(StepMovement.of(self.target, step_input), StepMovement.of(StepRef("First"), step_input))
        return graceful_complete().with_canceling_steps(self.target, StepRef("First")).with_canceling_sibling_steps(StepRef("Second"))


FIRST = RoutedStep[int]("First", StepRef("Second"))


class StepInstancesFlow(Flow):
    second = RoutedStep[str]("Second", FIRST)

    def get_steps(self):
        first = FIRST
        return StepList.start_step(first).other_steps(self.second)
