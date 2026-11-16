# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Sustainable Use License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta
from typing import TYPE_CHECKING, Any, Generic, TypeVar

from dex._utils import require_map_instance, validate_condition_id

if TYPE_CHECKING:
    from dex.channel import Channel, ChannelMap
    from dex.flow import Flow
    from dex.flow_options import SubFlowOptions

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class Condition:
    condition_id: str | None = None

    def __post_init__(self) -> None:
        validate_condition_id(self.condition_id)


@dataclass(frozen=True)
class TimerCondition(Condition):
    duration: timedelta = timedelta(0)

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.duration < timedelta(0):
            raise ValueError("timer duration must not be negative")


@dataclass(frozen=True)
class ChannelCondition(Condition, Generic[ValueT]):
    channel: Channel[ValueT] | ChannelMap[ValueT] | None = None
    instance: str | None = None
    at_least: int | None = None
    at_most: int | None = None

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.channel is None:
            raise ValueError("channel condition requires a channel")
        if self.instance is not None:
            require_map_instance(self.instance)
        if self.at_least is None and self.at_most is None:
            raise ValueError("channel condition requires a bound")
        if self.at_least is not None and self.at_least < 0:
            raise ValueError("at_least must not be negative")
        if self.at_most is not None and self.at_most < 0:
            raise ValueError("at_most must not be negative")
        if (
            self.at_least is not None
            and self.at_most is not None
            and self.at_most < self.at_least
        ):
            raise ValueError("at_most must not be below at_least")


@dataclass(frozen=True)
class SubFlowCondition(Condition):
    """Describe one independently running Flow as a durable Condition.

    Applications create this value through :meth:`dex.SubFlow.run`; the Worker
    validates that the target Flow instance is registered and has a starting Step.
    """

    flow: Flow[Any] | None = None
    input: object = None
    options: SubFlowOptions | None = None

    def __post_init__(self) -> None:
        super().__post_init__()
        if self.flow is None:
            raise ValueError("SubFlow condition requires a Flow")


@dataclass(frozen=True)
class GracefulCompletionCondition(Condition):
    """Describe a wait for a graceful completion request of the Flow.

    Applications create this value through
    :func:`graceful_completion_requested`.

    .. versionadded:: 1.45.4
    """


def graceful_completion_requested(*, condition_id: str | None = None) -> Condition:
    """Create a Condition satisfied once the Flow has a graceful completion request.

    A graceful completion request comes from ``Client.stop_flow`` with
    ``StopType.GRACEFUL_COMPLETE``, or from a Step that returns
    ``graceful_complete``. The request stays in force, so a Wait registered
    after it is satisfied at once. When the request arrives, Dex re-checks only
    the Waits that include this Condition. Other waiting Steps keep waiting and
    keep the Flow running.

    Use it to let a long-waiting Step end when someone asks the Flow to
    complete. A typical Step waits for the next message or this Condition with
    :meth:`Wait.any_of`. Its ``execute`` first handles a message that the Wait
    consumed. When there is none and
    ``Context.is_graceful_completion_condition_completed`` returns ``True``, it
    returns ``dead_end``. The Flow then completes once no other Step is active.

    In :meth:`Wait.any_of`, this Condition comes after Timers, Channels, and
    SubFlows, so a ready message still wins over the request. This Condition
    still reports completed when a message won and a request exists. Check the
    message first, or it is lost. It also works in :meth:`Wait.all_of` and in
    :meth:`Wait.any_combination_of`, where it needs ``condition_id``.

    A Wait may hold at most one graceful completion condition. A second one
    fails the Step's ``wait_for`` with ``InvalidStepResultError``.

    It requires Dex Server protocol version 7 or later, which server-zd 0.1.34
    and later releases provide. With an older Server, the Step's ``wait_for``
    fails with ``GracefulCompletionConditionUnsupportedError`` and the Step
    retries according to its retry policy.

    Args:
        condition_id: Optional stable identifier unique within the Wait tree.
            Required inside :meth:`Wait.any_combination_of`.

    Returns:
        A durable graceful completion Condition accepted by :class:`Wait`.

    Raises:
        ValueError: If ``condition_id`` is invalid.

    Examples:
        >>> from dex import (
        ...     Channel, Context, Step, StepDecision, Wait, dead_end,
        ...     go_to, graceful_completion_requested,
        ... )
        >>> USER_MESSAGES = Channel("user-messages", str)
        >>> class WaitForMessage(Step[None]):
        ...     def wait_for(self, context: Context, input: None) -> Wait:
        ...         return Wait.any_of(
        ...             USER_MESSAGES.for_one(),
        ...             graceful_completion_requested(),
        ...         )
        ...
        ...     def execute(self, context: Context, input: None) -> StepDecision:
        ...         messages = USER_MESSAGES.results(context)
        ...         if messages:
        ...             return go_to(HandleMessage, messages[0])
        ...         if context.is_graceful_completion_condition_completed():
        ...             return dead_end()
        ...         return go_to(WaitForMessage, None)

    .. versionadded:: 1.45.4
    """
    return GracefulCompletionCondition(condition_id=condition_id)


@dataclass(frozen=True)
class ConditionCombination:
    """Group Conditions that must become ready together.

    Pass combinations to :meth:`Wait.any_combination_of` when several alternative
    groups are acceptable.

    Attributes:
        conditions: The ordered Conditions in this all-of group.
    """

    conditions: tuple[Condition, ...]

    @staticmethod
    def of(*conditions: Condition) -> ConditionCombination:
        """Create a combination from one or more Conditions.

        Args:
            *conditions: Conditions that must all become ready together.

        Returns:
            A combination with Conditions in argument order.
        """
        return ConditionCombination(conditions)
