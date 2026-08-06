# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import timedelta

from dex import Context, Flow, Step, StepDecision, StepDef, StepOptions


class StateTimeoutStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        del context, input
        raise RuntimeError("timeout simulation")

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_method_timeout=timedelta(milliseconds=1))


class StateTimeoutFlow(Flow[int]):
    start = StateTimeoutStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)
