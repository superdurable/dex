# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from __future__ import annotations

from dataclasses import dataclass
from typing import Generic, Sequence, TypeVar, cast

from dex._utils import require_name
from dex.condition import ChannelCondition, Condition
from dex.context import Context

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class Channel(Generic[ValueT]):
    """Define a typed, durable singleton message stream owned by a Flow.

    Add the Channel to the Flow's ``PersistenceSchema``. Publishers append values;
    Steps use the condition factories to wait for a count and then inspect the
    selected values through ``results``.

    Attributes:
        name: The non-empty Channel name, unique within its Flow.
        value_type: The Python type of every published value.

    Examples:
        >>> approvals = Channel("approvals", str)
        >>> wait = Wait.until(approvals.for_n(2, condition_id="two-approvals"))
        >>> approvals.publish(context, "reviewer@example.com")
    """

    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        require_name(self.name)

    def publish(self, context: Context, value: ValueT) -> None:
        """Stage one value to append with the current handler decision.

        Args:
            context: The current Step or RPC Context.
            value: A value compatible with ``value_type``.
        """
        context._publish_channel(self, None, value)

    def size(self, context: Context) -> int:
        """Return the current number of queued values.

        Args:
            context: The current Step or RPC Context.

        Returns:
            The non-negative number of queued Channel values.
        """
        return context._channel_size(cast(Channel[object], self), None)

    def results(self, context: Context) -> Sequence[ValueT]:
        """Return values selected by the Step's satisfied Channel condition.

        Args:
            context: The current Step Context.

        Returns:
            An ordered, read-only sequence for this Step execution; it is empty
            when the Step did not wait on this Channel.
        """
        return context._channel_results(self, None)

    def for_one(self, *, condition_id: str | None = None) -> Condition:
        """Create a condition that consumes exactly one value.

        Args:
            condition_id: Optional stable identifier used by Timer and wait APIs.

        Returns:
            A Channel condition equivalent to ``for_range(at_least=1, at_most=1)``.
        """
        return self.for_range(at_least=1, at_most=1, condition_id=condition_id)

    def for_n(self, count: int, *, condition_id: str | None = None) -> Condition:
        """Create a condition that consumes exactly ``count`` values.

        Args:
            count: The required non-negative value count.
            condition_id: Optional stable condition identifier.

        Returns:
            A Channel condition with equal lower and upper bounds.
        """
        return self.for_range(
            at_least=count,
            at_most=count,
            condition_id=condition_id,
        )

    def at_least(
        self,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create a condition requiring at least ``count`` values.

        Args:
            count: The inclusive, non-negative lower bound.
            condition_id: Optional stable condition identifier.

        Returns:
            A Channel condition with no upper bound.
        """
        return self.for_range(at_least=count, condition_id=condition_id)

    def at_most(
        self,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create a condition consuming at most ``count`` available values.

        Args:
            count: The inclusive, non-negative upper bound.
            condition_id: Optional stable condition identifier.

        Returns:
            A Channel condition with no positive lower bound.
        """
        return self.for_range(at_most=count, condition_id=condition_id)

    def for_range(
        self,
        *,
        at_least: int | None = None,
        at_most: int | None = None,
        condition_id: str | None = None,
    ) -> Condition:
        """Create a bounded condition for queued Channel values.

        At least one bound is required. When both are present, ``at_least``
        cannot exceed ``at_most``. The condition is evaluated durably by Dex.

        Args:
            at_least: Optional inclusive lower bound.
            at_most: Optional inclusive upper bound.
            condition_id: Optional stable identifier unique within the Wait tree.

        Returns:
            A condition accepted by :class:`Wait` factories.
        """
        return ChannelCondition(
            condition_id=condition_id,
            channel=self,
            at_least=at_least,
            at_most=at_most,
        )


@dataclass(frozen=True)
class ChannelMap(Generic[ValueT]):
    """Define keyed Channel instances that share one typed schema.

    Each ``instance`` has an independent queue and may be waited on separately.
    Add the ChannelMap definition to the Flow's ``PersistenceSchema``.

    Attributes:
        name: The non-empty Channel name, unique within its Flow.
        value_type: The Python type of every published value.

    Examples:
        >>> events = ChannelMap("events", dict[str, str])
        >>> events.publish(context, "customer-42", {"kind": "paid"})
        >>> wait = Wait.until(events.for_one("customer-42"))
    """

    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        require_name(self.name)

    def publish(self, context: Context, instance: str, value: ValueT) -> None:
        """Stage a value to append to one ChannelMap instance.

        Args:
            context: The current Step or RPC Context.
            instance: The non-empty logical map key.
            value: A value compatible with ``value_type``.
        """
        context._publish_channel(self, instance, value)

    def size(self, context: Context, instance: str) -> int:
        """Return the queued value count for one instance.

        Args:
            context: The current Step or RPC Context.
            instance: The non-empty logical map key.

        Returns:
            The non-negative number of queued values for ``instance``.
        """
        return context._channel_size(cast(ChannelMap[object], self), instance)

    def results(self, context: Context, instance: str) -> Sequence[ValueT]:
        """Return values selected for one instance by the satisfied condition.

        Args:
            context: The current Step Context.
            instance: The non-empty logical map key.

        Returns:
            An ordered, read-only sequence for this Step execution.
        """
        return context._channel_results(self, instance)

    def for_one(
        self,
        instance: str,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create an instance condition that consumes exactly one value.

        Args:
            instance: The non-empty logical map key.
            condition_id: Optional stable condition identifier.

        Returns:
            A ChannelMap condition for one value.
        """
        return self.for_range(
            instance,
            at_least=1,
            at_most=1,
            condition_id=condition_id,
        )

    def for_n(
        self,
        instance: str,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create an instance condition that consumes exactly ``count`` values.

        Args:
            instance: The non-empty logical map key.
            count: The required non-negative value count.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition with equal lower and upper bounds.
        """
        return self.for_range(
            instance,
            at_least=count,
            at_most=count,
            condition_id=condition_id,
        )

    def at_least(
        self,
        instance: str,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create an instance condition requiring at least ``count`` values.

        Args:
            instance: The non-empty logical map key.
            count: The inclusive, non-negative lower bound.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition with no upper bound.
        """
        return self.for_range(
            instance,
            at_least=count,
            condition_id=condition_id,
        )

    def at_most(
        self,
        instance: str,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create an instance condition consuming at most ``count`` values.

        Args:
            instance: The non-empty logical map key.
            count: The inclusive, non-negative upper bound.
            condition_id: Optional stable condition identifier.

        Returns:
            A condition with no positive lower bound.
        """
        return self.for_range(
            instance,
            at_most=count,
            condition_id=condition_id,
        )

    def for_range(
        self,
        instance: str,
        *,
        at_least: int | None = None,
        at_most: int | None = None,
        condition_id: str | None = None,
    ) -> Condition:
        """Create a bounded condition for one ChannelMap instance.

        Args:
            instance: The non-empty logical map key.
            at_least: Optional inclusive lower bound.
            at_most: Optional inclusive upper bound.
            condition_id: Optional stable identifier unique within the Wait tree.

        Returns:
            A condition accepted by :class:`Wait` factories.
        """
        return ChannelCondition(
            condition_id=condition_id,
            channel=self,
            instance=instance,
            at_least=at_least,
            at_most=at_most,
        )
