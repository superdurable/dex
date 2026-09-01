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
    AsyncContext,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepDurability,
    StepList,
    StepOptions,
    Stream,
    Wait,
    graceful_complete,
)


class AsyncStreamTestStep(Step[None]):
    def __init__(self, progress: Stream[str]) -> None:
        self.progress = progress

    async def wait_for(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> Wait:
        del input
        progress = self.progress.buffered(context)
        progress.write("async-")
        progress.write("wait")
        progress.flush()
        await context.heartbeat("async-wait-checkpoint")
        return Wait.skip_immediately()

    async def execute(  # type: ignore[override]
        self,
        context: AsyncContext,
        input: None,
    ) -> StepDecision:
        del input
        progress = self.progress.buffered(context)
        progress.write("async-step-")
        progress.write("first")
        progress.flush()
        await context.heartbeat("async-checkpoint")
        progress.write("async-step-")
        progress.write("second")
        await context.heartbeat()
        return graceful_complete()

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            wait_for_durability=StepDurability.ASYNC,
            execute_durability=StepDurability.ASYNC,
        )


class AsyncStreamTestFlow(Flow[None]):
    def __init__(self) -> None:
        self.progress = Stream("async-stream-test-progress", str, 1 << 20)
        self.start = AsyncStreamTestStep(self.progress)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.progress)
