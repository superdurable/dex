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

from datetime import timedelta
from typing import Any, Callable, Generic, TypeVar

from dex import (
    Context,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    StepRef,
    StepTarget,
    Timer,
    Wait,
    force_complete,
    go_to,
    go_to_many,
    graceful_complete,
)

InputT = TypeVar("InputT")


class TypedInstanceStep(Step[InputT], Generic[InputT]):
    def __init__(
        self,
        step_type: str,
        input_type: object,
        decide: Callable[[InputT], StepDecision],
    ) -> None:
        self._step_type = step_type
        self._input_type = input_type
        self._decide = decide

    def get_step_type(self) -> str:
        return self._step_type

    def get_input_type(self) -> object | None:
        return self._input_type

    def wait_for(self, context: Context, input: InputT) -> Wait:
        return Wait.skip_immediately()

    def execute(self, context: Context, input: InputT) -> StepDecision:
        return self._decide(input)


class StepInstancesFlow(Flow[int]):
    def __init__(self) -> None:
        self.numbers = TypedInstanceStep[int]("Numbers", int, self._advance_number)
        self.strings = TypedInstanceStep[str]("Strings", str, self._advance_string)
        self.observed: list[int | str] = []

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.numbers).other_steps(self.strings)

    def _advance_number(self, input: int) -> StepDecision:
        assert type(input) is int
        self.observed.append(input)
        if input == 0:
            return go_to(self.strings, "first")
        if input == 1:
            return go_to_many(StepMovement.of(StepRef("Strings"), "second"))
        return (
            graceful_complete(input)
            .with_canceling_steps(self.strings, StepRef("Strings"))
            .with_canceling_sibling_steps(self.numbers, StepRef("Numbers"))
        )

    def _advance_string(self, input: str) -> StepDecision:
        assert type(input) is str
        self.observed.append(input)
        if input == "first":
            return go_to(StepRef("Numbers"), 1)
        return go_to_many(StepMovement.of(self.numbers, 2))


class FailingInstanceStep(Step[None]):
    def __init__(self, target: StepTarget[None]) -> None:
        self._target = target

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1)
        ).on_execute_failure_proceed_to(self._target)

    def execute(self, context: Context, input: None) -> StepDecision:
        raise RuntimeError("route exhausted execution to recovery")


class InstanceRecoveryFlow(Flow[None]):
    def __init__(self, reference: bool) -> None:
        self.recovery = TypedInstanceStep[None]("Recovery", type(None), self._complete)
        self.failure = FailingInstanceStep(
            StepRef("Recovery") if reference else self.recovery
        )

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.failure).other_steps(self.recovery)

    def _complete(self, input: None) -> StepDecision:
        assert input is None
        return graceful_complete("recovered")


class NumberFailureRecoveryFlow(Flow[int]):
    def __init__(self, recovery_input_type: object) -> None:
        self.recovery = TypedInstanceStep[Any](
            "Recovery", recovery_input_type, graceful_complete
        )
        self.failure = FailingNumberStep(self.recovery)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.failure).other_steps(self.recovery)


class FailingNumberStep(Step[int]):
    def __init__(self, target: StepTarget[Any]) -> None:
        self._target = target

    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_retry=RetryPolicy(maximum_attempts=1)
        ).on_execute_failure_proceed_to(self._target)

    def execute(self, context: Context, input: int) -> StepDecision:
        raise RuntimeError("route exhausted execution to recovery")


class AwaitTimeoutStep(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(Timer.by_duration(timedelta(minutes=5)))

    def execute(self, context: Context, input: None) -> StepDecision:
        return graceful_complete("timer fired")


class TimeoutInstanceRecoveryFlow(Flow[None]):
    def __init__(self) -> None:
        self.waiting = AwaitTimeoutStep()
        self.recovery = TypedInstanceStep[None](
            "TimeoutRecovery", type(None), self._recover
        )
        self.alternate = TypedInstanceStep[None](
            "AlternateRecovery", type(None), self._alternate
        )

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.waiting).other_steps(
            self.recovery, self.alternate
        )

    def handle_timeout(self, context: Context) -> StepDecision:
        raise RuntimeError("route exhausted timeout handling to recovery")

    def _recover(self, input: None) -> StepDecision:
        assert input is None
        return force_complete("recovered")

    def _alternate(self, input: None) -> StepDecision:
        return force_complete("alternate")
