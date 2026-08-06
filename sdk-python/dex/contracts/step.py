# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, replace
from datetime import timedelta
from enum import Enum
from typing import Any, Awaitable, Generic, Iterator, TypeAlias, TypeVar

from dex.contracts.state import (
    AttributeLock,
    Channel,
    ChannelMap,
    Context,
    Wait,
)

InputT = TypeVar("InputT")
ResultT = TypeVar("ResultT")
StartT = TypeVar("StartT")
MaybeAwaitable: TypeAlias = ResultT | Awaitable[ResultT]


class StepDurability(Enum):
    DEFAULT = "default"
    SYNC = "sync"
    ASYNC = "async"


class WaitForFailurePolicy(Enum):
    FAIL_FLOW = "fail_flow"
    PROCEED = "proceed"


@dataclass(frozen=True)
class RetryPolicy:
    initial_interval: timedelta | None = None
    backoff_coefficient: float = 0.0
    maximum_interval: timedelta | None = None
    maximum_attempts: int = 0
    total_duration: timedelta | None = None


@dataclass(frozen=True)
class StepOptions:
    wait_for_method_timeout: timedelta | None = None
    execute_method_timeout: timedelta | None = None
    wait_for_retry: RetryPolicy | None = None
    execute_retry: RetryPolicy | None = None
    wait_for_failure: WaitForFailurePolicy = WaitForFailurePolicy.FAIL_FLOW
    wait_for_durability: StepDurability = StepDurability.DEFAULT
    execute_durability: StepDurability = StepDurability.DEFAULT
    wait_for_lock_attributes: tuple[AttributeLock, ...] = ()
    execute_lock_attributes: tuple[AttributeLock, ...] = ()
    _execute_failure_target: Step[Any] | None = None
    _execute_failure_options: StepOptions | None = None

    def on_execute_failure_proceed_to(
        self,
        step: Step[Any],
        options: StepOptions | None = None,
    ) -> StepOptions:
        return replace(
            self,
            _execute_failure_target=step,
            _execute_failure_options=options,
        )


class Step(Generic[InputT], ABC):
    @abstractmethod
    def execute(
        self,
        context: Context,
        input: InputT,
    ) -> MaybeAwaitable[StepDecision]:
        raise NotImplementedError

    def wait_for(self, context: Context, input: InputT) -> MaybeAwaitable[Wait]:
        del context, input
        raise RuntimeError("framework must skip the default wait_for")

    def get_step_type(self) -> str:
        return type(self).__name__

    def get_step_options(self) -> StepOptions | None:
        return None


@dataclass(frozen=True)
class _StepDef:
    step: Step[Any]
    is_start_step: bool


@dataclass(frozen=True)
class StepList(Generic[StartT]):
    _definitions: tuple[_StepDef, ...]

    @classmethod
    def empty(cls) -> StepList[StartT]:
        return cls(())

    @staticmethod
    def start_step(step: Step[StartT]) -> StepList[StartT]:
        return StepList((_StepDef(step, True),))

    @classmethod
    def without_start_step(cls, *steps: Step[Any]) -> StepList[StartT]:
        return cls(tuple(_StepDef(step, False) for step in steps))

    def other_steps(self, *steps: Step[Any]) -> StepList[StartT]:
        return StepList(
            self._definitions + tuple(_StepDef(step, False) for step in steps)
        )

    def __iter__(self) -> Iterator[_StepDef]:
        return iter(self._definitions)


@dataclass(frozen=True)
class StepMovement(Generic[InputT]):
    step: Step[InputT]
    input: InputT
    options: StepOptions | None = None

    @staticmethod
    def of(
        step: Step[InputT],
        input: InputT,
        *,
        options: StepOptions | None = None,
    ) -> StepMovement[InputT]:
        return StepMovement(step, input, options)


class DecisionKind(Enum):
    NEXT = "next"
    GRACEFUL_COMPLETE = "graceful_complete"
    FORCE_COMPLETE = "force_complete"
    FORCE_COMPLETE_IF_CHANNELS_EMPTY = "force_complete_if_channels_empty"
    FORCE_FAIL = "force_fail"
    DEAD_END = "dead_end"


@dataclass(frozen=True)
class StepDecision:
    kind: DecisionKind
    movements: tuple[StepMovement[Any], ...] = ()
    output: object | None = None
    reason: str = ""
    empty_channels: tuple[Channel[Any] | ChannelMap[Any], ...] = ()
    fallback: StepMovement[Any] | None = None


def go_to(step: Step[InputT], input: InputT) -> StepDecision:
    return go_to_multi(StepMovement.of(step, input))


def go_to_multi(*movements: StepMovement[Any]) -> StepDecision:
    return StepDecision(DecisionKind.NEXT, movements=movements)


def graceful_complete(output: object | None = None) -> StepDecision:
    return StepDecision(DecisionKind.GRACEFUL_COMPLETE, output=output)


def force_complete(output: object | None = None) -> StepDecision:
    return StepDecision(DecisionKind.FORCE_COMPLETE, output=output)


def force_complete_when_channels_empty(
    output: object,
    fallback: StepMovement[Any],
    *channels: Channel[Any] | ChannelMap[Any],
) -> StepDecision:
    return StepDecision(
        DecisionKind.FORCE_COMPLETE_IF_CHANNELS_EMPTY,
        output=output,
        empty_channels=channels,
        fallback=fallback,
    )


def force_fail(reason: str) -> StepDecision:
    return StepDecision(DecisionKind.FORCE_FAIL, reason=reason)


def dead_end() -> StepDecision:
    return StepDecision(DecisionKind.DEAD_END)
