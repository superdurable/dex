# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Super Durable Source License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from datetime import timedelta
from typing import Generator

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    StepOutput,
    Stream,
    Wait,
    graceful_complete,
    heartbeat,
)


class StepStreamingStep(Step[None]):
    def __init__(self, progress: Stream[str]) -> None:
        self.progress = progress

    def wait_for(
        self,
        context: Context,
        input: None,
    ) -> Generator[StepOutput, None, Wait]:
        del input
        yield heartbeat("wait-checkpoint")
        yield self.progress.write(context, "wait-stream")
        return Wait.skip_immediately()

    def execute(
        self,
        context: Context,
        input: None,
    ) -> Generator[StepOutput, None, StepDecision]:
        del input
        if context.attempt == 1:
            yield heartbeat("execute-checkpoint")
            yield self.progress.write(context, "attempt-1-first")
            yield self.progress.write(context, "attempt-1-second")
            raise RuntimeError("retry after persisted checkpoint")
        if context.attempt == 2:
            if not context.has_last_heartbeat_value():
                raise AssertionError("attempt 2 checkpoint is absent")
            if context.get_last_heartbeat_value(str) != "execute-checkpoint":
                raise AssertionError("attempt 2 checkpoint is incorrect")
            yield heartbeat()
            yield self.progress.write(context, "attempt-2-after-clear")
            raise RuntimeError("retry after clearing checkpoint")
        if context.attempt == 3:
            if context.has_last_heartbeat_value():
                raise AssertionError("attempt 3 checkpoint was not cleared")
            yield heartbeat(None)
            raise RuntimeError("retry after persisted None")
        if not context.has_last_heartbeat_value():
            raise AssertionError("attempt 4 None checkpoint is absent")
        if context.get_last_heartbeat_value(type(None)) is not None:
            raise AssertionError("attempt 4 None checkpoint is incorrect")
        return graceful_complete("recovered")

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            heartbeat_timeout=timedelta(seconds=10),
            execute_retry=RetryPolicy(
                initial_interval=timedelta(seconds=1),
                maximum_attempts=4,
            ),
        )


class StepStreamingFlow(Flow[None]):
    def __init__(self) -> None:
        self.progress = Stream("step-streaming-progress", str, 1 << 20)
        self.start = StepStreamingStep(self.progress)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.start)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.progress)
