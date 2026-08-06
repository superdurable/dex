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
    Attribute,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
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

    def get_steps(self) -> StepList[bool]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.counter, self.signal, self.internal)

    @rpc
    def publish_to_internal_channel(self, context: Context) -> None:
        self.internal.publish(context, None)
