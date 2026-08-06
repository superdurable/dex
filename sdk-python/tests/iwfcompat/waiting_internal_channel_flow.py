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
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
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

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(channels=(self.channel,))
