# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex import (
    Channel,
    ChannelMap,
    ConditionCombination,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepDef,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)


class SignalCombinationStep(Step[int]):
    def __init__(
        self,
        second: Channel[int],
        third: Channel[int],
        signal_map: ChannelMap[int],
    ) -> None:
        self.second = second
        self.third = third
        self.signal_map = signal_map

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.any_combination_of(
            ConditionCombination.of(
                self.second.for_one(condition_id="signal-2"),
                Timer.by_duration(
                    timedelta(seconds=10),
                    condition_id="test-timer-id",
                ),
            ),
            ConditionCombination.of(
                self.third.for_n(2),
                self.signal_map.for_one("one"),
            ),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input + self.third.size(context))


class SignalFirstStep(Step[int]):
    def __init__(self, first: Channel[int], combination: SignalCombinationStep) -> None:
        self.first = first
        self.combination = combination

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.any_of(self.first.for_one(condition_id="test-signal-id"))

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to(self.combination, input + self.first.results(context)[0])


class SignalFlow(Flow[int]):
    def __init__(self) -> None:
        self.first = Channel("signal-1", int)
        self.second = Channel("signal-2", int)
        self.third = Channel("signal-3", int)
        self.signal_map = ChannelMap("signal-map", int)
        self.combination = SignalCombinationStep(
            self.second,
            self.third,
            self.signal_map,
        )
        self.start = SignalFirstStep(self.first, self.combination)

    def get_steps(self) -> tuple[StepDef, ...]:
        return (
            StepDef.start_step(self.start),
            StepDef.non_start_step(self.combination),
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema(
            channels=(self.first, self.second, self.third, self.signal_map)
        )
