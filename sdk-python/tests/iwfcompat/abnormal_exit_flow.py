# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import Context, Flow, Step, StepDecision, StepDef


class AbnormalExitStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("abnormal exit")


class AbnormalExitFlow(Flow[int]):
    start = AbnormalExitStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)
