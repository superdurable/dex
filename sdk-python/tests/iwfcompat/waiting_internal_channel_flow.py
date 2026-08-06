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
    Step,
    StepDecision,
    StepDef,
    Wait,
    graceful_complete,
)


class WaitingInternalStep(Step[int]):
    def __init__(self, channel: Channel[int]) -> None:
        self.channel = channel

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.all_of(self.channel.for_n(2))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input + sum(self.channel.results(context)))


class WaitingInternalChannelFlow(Flow[int]):
    def __init__(self) -> None:
        self.channel = Channel("waiting-channel", int)
        self.start = WaitingInternalStep(self.channel)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(channels=(self.channel,))
