# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from dex import (
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepDef,
    StepMovement,
    Wait,
    force_complete_when_channels_empty,
    rpc,
)


class ConditionalStep(Step[bool]):
    def __init__(
        self,
        counter: Attribute[int],
        signal: Channel[None],
        internal: Channel[None],
    ) -> None:
        self.counter = counter
        self.signal = signal
        self.internal = internal

    def wait_for(self, context: Context, input: bool) -> Wait:
        del context
        return Wait.any_of((self.signal if input else self.internal).for_one())

    def execute(self, context: Context, input: bool) -> StepDecision:
        next_value = self.counter.get(context) + 1
        self.counter.set(context, next_value)
        selected = self.signal if input else self.internal
        return force_complete_when_channels_empty(
            next_value,
            StepMovement.of(self, input),
            selected,
        )


class ConditionalCompleteFlow(Flow[bool]):
    def __init__(self) -> None:
        self.signal = Channel[None]("test-signal-channel", type(None))
        self.internal = Channel[None]("test-internal-channel", type(None))
        self.counter = Attribute("counter", int)
        self.start = ConditionalStep(self.counter, self.signal, self.internal)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (StepDef.start_step(self.start),)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(
            attributes=(self.counter,),
            channels=(self.signal, self.internal),
        )

    @rpc
    def publish_to_internal_channel(self, context: Context) -> None:
        self.internal.publish(context, None)
