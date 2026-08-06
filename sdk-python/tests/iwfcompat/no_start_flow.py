# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import (
    Context,
    Flow,
    RPCResult,
    Step,
    StepDecision,
    StepDef,
    StepMovement,
    graceful_complete,
    rpc,
)


class TriggeredStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete(1)


class NoStartFlow(Flow[None]):
    def __init__(self) -> None:
        self.triggered = TriggeredStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.non_start_step(self.triggered),)

    @rpc
    def invoke(self, context: Context, input: str) -> RPCResult[int]:
        del context, input
        return RPCResult(1, (StepMovement.of(self.triggered, None),))
