# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from typing import Generator

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepOutput,
    Stream,
    graceful_complete,
)


class StreamTestStep(Step[None]):
    def __init__(self, progress: Stream[str]) -> None:
        self.progress = progress

    def execute(
        self,
        context: Context,
        input: None,
    ) -> Generator[StepOutput, None, StepDecision]:
        del input
        yield self.progress.write(context, "step-progress")
        return graceful_complete()


class StreamTestFlow(Flow[None]):
    def __init__(self) -> None:
        self.progress = Stream("stream-test-progress", str, 1 << 20)
        self.start = StreamTestStep(self.progress)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.progress)
