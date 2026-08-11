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
from typing import Any, Generic, Iterator, TypeVar

from dex.attribute import AttributeLock
from dex.channel import Channel, ChannelMap
from dex.context import Context
from dex.wait import Wait

InputT = TypeVar("InputT")
StartT = TypeVar("StartT")


class StepDurability(Enum):
    """Control when a Step handler result is durably acknowledged.

    Attributes:
        DEFAULT: Use the Flow or server durability policy.
        SYNC: Persist the result before acknowledging the Worker call.
        ASYNC: Allow acknowledgement before persistence completes.
    """

    DEFAULT = "default"
    SYNC = "sync"
    ASYNC = "async"


class WaitForFailurePolicy(Enum):
    """Control what Dex does when a Step's ``wait_for`` method fails.

    Attributes:
        FAIL_FLOW: Fail the Flow without invoking ``execute``.
        PROCEED: Invoke ``execute`` and expose the failure through Context.
    """

    FAIL_FLOW = "fail_flow"
    PROCEED = "proceed"


@dataclass(frozen=True)
class RetryPolicy:
    """Configure exponential retries for a Step handler or Flow.

    A ``None`` duration or zero numeric value uses the server default.
    Attempts include the initial call. Retries stop at the first configured limit.

    Attributes:
        initial_interval: Delay before the first retry.
        backoff_coefficient: Interval multiplier; zero uses the server default.
        maximum_interval: Upper bound for one retry delay.
        maximum_attempts: Total attempt limit; zero uses the server default.
        total_duration: Overall elapsed-time limit for all attempts.
    """

    initial_interval: timedelta | None = None
    backoff_coefficient: float = 0.0
    maximum_interval: timedelta | None = None
    maximum_attempts: int = 0
    total_duration: timedelta | None = None


@dataclass(frozen=True)
class StepOptions:
    """Configure timeouts, retries, durability, locks, and failure routing for a Step.

    Fields set to ``None`` or ``DEFAULT`` use Flow or server policy. Attribute
    locks are acquired separately for ``wait_for`` and ``execute`` and must reference
    definitions in the containing Flow's ``PersistenceSchema``.

    Attributes:
        wait_for_method_timeout: Maximum duration of one ``wait_for`` attempt.
        execute_method_timeout: Maximum duration of one ``execute`` attempt.
        wait_for_retry: Optional retry policy for ``wait_for``.
        execute_retry: Optional retry policy for ``execute``.
        wait_for_failure: Behavior after all ``wait_for`` attempts fail.
        wait_for_durability: Durability used for the ``wait_for`` result.
        execute_durability: Durability used for the ``execute`` result.
        wait_for_lock_attributes: Attribute locks held during ``wait_for``.
        execute_lock_attributes: Attribute locks held during ``execute``.
    """

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
        """Return a copy that routes exhausted ``execute`` retries to another Step.

        Args:
            step: The registered fallback Step.
            options: Optional options applied to the fallback movement.

        Returns:
            A new StepOptions object with failure routing configured.
        """
        return replace(
            self,
            _execute_failure_target=step,
            _execute_failure_options=options,
        )


class Step(Generic[InputT], ABC):
    """Define one typed unit of durable Flow application logic.

    Subclasses implement ``execute`` and may implement ``wait_for``. Handler methods
    must remain deterministic with respect to their inputs and Context state; external
    side effects belong behind idempotent application integrations.

    Examples:
        >>> class Charge(Step[ChargeInput]):
        ...     def execute(self, context: Context, input: ChargeInput) -> StepDecision:
        ...         status.set(context, "charged")
        ...         return graceful_complete(input.order_id)
    """

    @abstractmethod
    def execute(
        self,
        context: Context,
        input: InputT,
    ) -> StepDecision:
        """Execute application logic and return the next durable decision.

        Args:
            context: Execution metadata and decision-local persistence operations.
            input: The decoded value passed by the incoming Step movement.

        Returns:
            A StepDecision describing movements or Flow completion.

        Raises:
            Exception: Application exceptions are retried according to StepOptions
                and may ultimately fail or reroute the Flow.
        """
        raise NotImplementedError

    def wait_for(self, context: Context, input: InputT) -> Wait:
        """Describe durable conditions that must be ready before ``execute``.

        Override this method only when the Step waits. Dex skips the base
        implementation and invokes ``execute`` immediately.

        Args:
            context: Execution metadata and decision-local persistence operations.
            input: The same decoded input later passed to ``execute``.

        Returns:
            A durable Wait condition tree.
        """
        del context, input
        raise RuntimeError("framework must skip the default wait_for")

    def get_step_type(self) -> str:
        """Return the name used to register and address this Step.

        Override it to decouple the protocol Step type from the Python class name.

        Returns:
            A non-empty name unique within the containing Flow.
        """
        return type(self).__name__

    def get_step_options(self) -> StepOptions | None:
        """Return this Step's static execution options.

        Returns:
            Step-specific options, or ``None`` to use Flow and server defaults.
        """
        return None


@dataclass(frozen=True)
class _StepDef:
    step: Step[Any]
    is_start_step: bool


@dataclass(frozen=True)
class StepList(Generic[StartT]):
    """Build the ordered Step definitions for a Flow.

    A Flow may have zero or one starting Step. Other Steps are reachable only through
    decisions, RPC results, or failure routing.
    """

    _definitions: tuple[_StepDef, ...]

    @classmethod
    def empty(cls) -> StepList[StartT]:
        """Create a StepList with no starting or other Steps.

        Returns:
            An empty StepList.
        """
        return cls(())

    @staticmethod
    def start_step(step: Step[StartT]) -> StepList[StartT]:
        """Create a StepList whose first definition is the Flow starting Step.

        Args:
            step: The Step receiving ``start_flow`` input.

        Returns:
            A StepList with exactly one starting Step.
        """
        return StepList((_StepDef(step, True),))

    @classmethod
    def without_start_step(cls, *steps: Step[Any]) -> StepList[StartT]:
        """Create a StepList with only non-starting Steps.

        Args:
            *steps: Registered Steps reachable through other entry paths.

        Returns:
            A StepList that requires ``None`` start input.
        """
        return cls(tuple(_StepDef(step, False) for step in steps))

    def other_steps(self, *steps: Step[Any]) -> StepList[StartT]:
        """Return a copy with additional non-starting Steps appended.

        Args:
            *steps: Steps to append in registration order.

        Returns:
            A new StepList.
        """
        return StepList(
            self._definitions + tuple(_StepDef(step, False) for step in steps)
        )

    def __iter__(self) -> Iterator[_StepDef]:
        """Iterate internal definitions in stable registration order.

        Returns:
            An iterator over the registered Step definitions.
        """
        return iter(self._definitions)


@dataclass(frozen=True)
class StepMovement(Generic[InputT]):
    """Schedule one registered Step with typed input and optional options.

    Attributes:
        step: The destination Step instance from the containing Registry.
        input: The value decoded by the destination Step's input codec.
        options: Optional per-movement StepOptions override.
    """

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
        """Create a typed movement to a Step.

        Args:
            step: The registered destination Step.
            input: A value compatible with the Step's input annotation.
            options: Optional per-movement options.

        Returns:
            A StepMovement.
        """
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
    """Describe the durable state transition returned by ``Step.execute``.

    Prefer the module-level factory functions instead of constructing this class
    directly; they populate the fields required for each decision kind.

    Attributes:
        kind: The transition behavior interpreted by Dex.
        movements: Destination Steps for a next decision.
        output: Optional Flow completion value.
        reason: Human-readable force-failure reason.
        empty_channels: Channels that must be empty for conditional completion.
        fallback: Movement used when conditional completion cannot proceed.
    """

    kind: DecisionKind
    movements: tuple[StepMovement[Any], ...] = ()
    output: object | None = None
    reason: str = ""
    empty_channels: tuple[Channel[Any] | ChannelMap[Any], ...] = ()
    fallback: StepMovement[Any] | None = None


def go_to(step: Step[InputT], input: InputT) -> StepDecision:
    """Create a decision that schedules one next Step.

    Args:
        step: The registered destination Step.
        input: A value compatible with the destination input annotation.

    Returns:
        A next decision containing one StepMovement.
    """
    return go_to_multi(StepMovement.of(step, input))


def go_to_multi(*movements: StepMovement[Any]) -> StepDecision:
    """Create a decision that schedules several next Steps.

    Args:
        *movements: Typed destination movements applied in argument order.

    Returns:
        A next decision with all movements.
    """
    return StepDecision(DecisionKind.NEXT, movements=movements)


def graceful_complete(output: object | None = None) -> StepDecision:
    """Request successful completion after already-scheduled Steps finish.

    Args:
        output: Optional codec-supported Flow result.

    Returns:
        A graceful-completion decision.
    """
    return StepDecision(DecisionKind.GRACEFUL_COMPLETE, output=output)


def force_complete(output: object | None = None) -> StepDecision:
    """Request immediate successful completion of the Flow.

    Args:
        output: Optional codec-supported Flow result.

    Returns:
        A force-completion decision that does not await other active Steps.
    """
    return StepDecision(DecisionKind.FORCE_COMPLETE, output=output)


def force_complete_if_channels_empty(
    output: object,
    fallback: StepMovement[Any],
    *channels: Channel[Any] | ChannelMap[Any],
) -> StepDecision:
    """Complete only when selected Channels are empty, otherwise schedule fallback.

    Args:
        output: The codec-supported completion result.
        fallback: Movement scheduled when any selected Channel is non-empty.
        *channels: Registered Channel or ChannelMap definitions to inspect.

    Returns:
        A conditional force-completion decision.
    """
    return StepDecision(
        DecisionKind.FORCE_COMPLETE_IF_CHANNELS_EMPTY,
        output=output,
        empty_channels=channels,
        fallback=fallback,
    )


def force_fail(reason: str) -> StepDecision:
    """Request immediate Flow failure with an application reason.

    Args:
        reason: Non-empty human-readable failure detail.

    Returns:
        A force-failure decision.
    """
    return StepDecision(DecisionKind.FORCE_FAIL, reason=reason)


def dead_end() -> StepDecision:
    """End this execution path without scheduling work or closing the Flow.

    Returns:
        A dead-end decision; other active paths may continue.
    """
    return StepDecision(DecisionKind.DEAD_END)
