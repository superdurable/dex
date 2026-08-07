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
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)


class SignalCombinationStep(Step[int]):
    def __init__(
        self,
        first: Channel[int],
        second: Channel[int],
        third: Channel[None],
        signal_map: ChannelMap[int],
    ) -> None:
        self.first = first
        self.second = second
        self.third = third
        self.signal_map = signal_map

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.any_combination_of(
            ConditionCombination.of(
                self.first.for_one(condition_id="signal-1"),
                self.third.for_one(condition_id="signal-3"),
                self.signal_map.for_one("one"),
                Timer.by_duration(
                    timedelta(days=365),
                    condition_id="test-timer-id",
                ),
            )
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        if self.second.results(context):
            raise RuntimeError("second signal should still be waiting")
        if len(self.third.results(context)) != 1:
            raise RuntimeError("null signal was not received")
        if len(self.signal_map.results(context, "one")) != 1:
            raise RuntimeError("mapped signal was not received")
        if not context.has_timer_fired():
            raise RuntimeError("timer was not fired")
        return graceful_complete(input + self.first.results(context)[0])


class SignalFirstStep(Step[int]):
    def __init__(
        self,
        first: Channel[int],
        second: Channel[int],
        combination: SignalCombinationStep,
    ) -> None:
        self.first = first
        self.second = second
        self.combination = combination

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        return Wait.any_of(
            self.first.for_one(condition_id="test-signal-id-1"),
            self.second.for_one(condition_id="test-signal-id-2"),
        )

    def execute(self, context: Context, input: int) -> StepDecision:
        if self.second.results(context):
            raise RuntimeError("second signal should still be waiting")
        return go_to(self.combination, input + self.first.results(context)[0])


class SignalFlow(Flow[int]):
    def __init__(self) -> None:
        self.first = Channel("signal-1", int)
        self.second = Channel("signal-2", int)
        self.third = Channel("signal-3", type(None))
        self.signal_map = ChannelMap("signal-map", int)
        self.combination = SignalCombinationStep(
            self.first,
            self.second,
            self.third,
            self.signal_map,
        )
        self.start = SignalFirstStep(self.first, self.second, self.combination)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start).other_steps(self.combination)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(
            self.first,
            self.second,
            self.third,
            self.signal_map,
        )
