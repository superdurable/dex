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
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    Wait,
    graceful_complete,
)


class AnyCombinationStep(Step[int]):
    def __init__(
        self,
        first: Channel[int],
        second: Channel[int],
        third: Channel[int],
    ) -> None:
        self.first = first
        self.second = second
        self.third = third

    def wait_for(self, context: Context, input: int) -> Wait:
        del context, input
        raise ValueError("Found unknown condition ID in the combination list")

    def execute(self, context: Context, input: int) -> StepDecision:
        del context
        return graceful_complete(input)

    def get_step_options(self) -> StepOptions:
        return StepOptions(wait_for_retry=RetryPolicy(maximum_attempts=1))


class AnyCombinationFailFlow(Flow[int]):
    def __init__(self) -> None:
        self.first = Channel("test-signal-1", int)
        self.second = Channel("test-signal-2", int)
        self.third = Channel("test-signal-3", int)
        self.start = AnyCombinationStep(self.first, self.second, self.third)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.first, self.second, self.third)
