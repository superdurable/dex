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
    ChannelMap,
    ConditionCombination,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    dead_end,
    go_to_multi,
    graceful_complete,
)


class ConsumeStep(Step[int]):
    def __init__(self, first: Channel[int], channel_map: ChannelMap[int]) -> None:
        self.first = first
        self.channel_map = channel_map

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.any_combination_of(
            ConditionCombination.of(self.first.for_one(condition_id="first")),
            ConditionCombination.of(self.channel_map.for_one("one")),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input + len(self.first.results(context)))


class PublishStep(Step[int]):
    def __init__(self, first: Channel[int], channel_map: ChannelMap[int]) -> None:
        self.first = first
        self.channel_map = channel_map

    def execute(self, context: Context, input: int) -> StepDecision:
        self.first.publish(context, input)
        self.channel_map.publish(context, "one", input)
        return dead_end()


class ForkStep(Step[int]):
    def __init__(self, consumer: ConsumeStep, publisher: PublishStep) -> None:
        self.consumer = consumer
        self.publisher = publisher

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return go_to_multi(
            StepMovement.of(self.consumer, input),
            StepMovement.of(self.publisher, input),
        )


class BasicInternalChannelFlow(Flow[int]):
    def __init__(self) -> None:
        self.first_channel = Channel("test-inter-state-channel-1", int)
        self.channel_map = ChannelMap("test-inter-state-channel-map", int)
        self.consumer = ConsumeStep(self.first_channel, self.channel_map)
        self.publisher = PublishStep(self.first_channel, self.channel_map)
        self.start = ForkStep(self.consumer, self.publisher)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(
            self.consumer, self.publisher
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(channels=(self.first_channel, self.channel_map))
