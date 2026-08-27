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
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Stream,
    graceful_complete,
)


class AsyncStreamTestStep(Step[None]):
    def __init__(self, progress: Stream[str]) -> None:
        self.progress = progress

    async def execute(  # type: ignore[override]
        self,
        context: Context,
        input: None,
    ) -> StepDecision:
        del input
        await self.progress.write(context, "async-step-progress")
        return graceful_complete()


class AsyncStreamTestFlow(Flow[None]):
    def __init__(self) -> None:
        self.progress = Stream("async-stream-test-progress", str, 1 << 20)
        self.start = AsyncStreamTestStep(self.progress)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.progress)
