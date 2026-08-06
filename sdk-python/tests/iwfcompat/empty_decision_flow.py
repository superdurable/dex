# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Context, Flow, Step, StepDecision, StepDef, go_to_multi


class EmptyDecisionStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        return go_to_multi()


class EmptyDecisionFlow(Flow[int]):
    start = EmptyDecisionStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)
