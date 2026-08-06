# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepDef,
    dead_end,
    rpc,
)


class DeadEndStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return dead_end()


class DeadEndFlow(Flow[None]):
    idle_signal = Channel[None]("idle-signal", type(None))

    def __init__(self) -> None:
        self.start = DeadEndStep()

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(channels=(self.idle_signal,))

    @rpc
    def signal_size(self, context: Context) -> RPCResult[int]:
        return RPCResult(self.idle_signal.size(context))

    @rpc
    def publish_internal(self, context: Context) -> RPCResult[int]:
        self.idle_signal.publish(context, None)
        return RPCResult(self.idle_signal.size(context))
