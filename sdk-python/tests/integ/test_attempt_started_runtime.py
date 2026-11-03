# Portions of this file are derived from indeedeng/iwf-java-sdk.
# Those portions are licensed under the Apache License, Version 2.0.
# See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
#
# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications are licensed under the Sustainable Use License 1.0.
# Third-Party Materials remain under the Apache License, Version 2.0.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

import asyncio
from datetime import timedelta

import pytest

from dex import (
    AsyncContext,
    Flow,
    RetryPolicy,
    Step,
    StepAttemptProgress,
    StepDecision,
    StepList,
    StepOptions,
    graceful_complete,
)

from .async_environment import AsyncDexDevTestEnvironment
from .shared import unique_id


class RecordRecoveryProgress(Step[str]):
    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: str
    ) -> StepDecision:
        error = context.recovery_error
        assert error is not None
        return graceful_complete(error.attempt_progress.name)


class MarkedEffect(Step[str]):
    """Fails after or before marking the attempt started, or completes after the mark."""

    def __init__(self, recovery: RecordRecoveryProgress) -> None:
        self._recovery = recovery
        self.effects = 0

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1),
        ).on_execute_failure_proceed_to(self._recovery)

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: str
    ) -> StepDecision:
        if input == "fail_before_mark":
            raise RuntimeError("the effect never started")
        await context.mark_attempt_started()
        self.effects += 1
        if input == "fail_after_mark":
            raise RuntimeError("the effect may have run")
        return graceful_complete("completed")


class MarkedEffectFlow(Flow[str]):
    def __init__(self) -> None:
        self.recovery = RecordRecoveryProgress()
        self.effect = MarkedEffect(self.recovery)

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.effect).other_steps(self.recovery)


@pytest.mark.parametrize(
    ("scenario", "expected_output", "expected_effects"),
    [
        ("complete", "completed", 1),
        ("fail_after_mark", StepAttemptProgress.STARTED.name, 1),
        ("fail_before_mark", StepAttemptProgress.ASSIGNED.name, 0),
    ],
)
def test_mark_attempt_started(
    scenario: str, expected_output: str, expected_effects: int
) -> None:
    asyncio.run(_run_mark_attempt_started(scenario, expected_output, expected_effects))


async def _run_mark_attempt_started(
    scenario: str, expected_output: str, expected_effects: int
) -> None:
    flow = MarkedEffectFlow()
    async with AsyncDexDevTestEnvironment(
        flow, allow_async_handlers=True
    ) as environment:
        flow_id = unique_id(f"python-attempt-started-{scenario}")
        await environment.client.start_flow(flow, flow_id, scenario)
        result = await environment.client.wait_for_flow(flow_id, timedelta(seconds=30))
        assert result.single_output(str) == expected_output
        assert flow.effect.effects == expected_effects
