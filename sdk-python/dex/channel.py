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
from typing import Any, Generic, Sequence, TypeVar, cast
from urllib.parse import quote

from dex._utils import require_name, require_persistence_definition_name
from dex.condition import ChannelCondition, Condition
from dex.context import Context

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class ChannelLoad:
    """Select one Channel's pending messages for an RPC snapshot.

    Attributes:
        channel: The exact Channel definition registered with the Flow.
    """

    channel: Channel[Any]

    @property
    def selector(self) -> str:
        """Return the Channel name sent to Dex.

        Returns:
            The registered singleton Channel name.
        """
        return self.channel.name


@dataclass(frozen=True)
class ChannelMapLoad:
    """Select ChannelMap pending messages for an RPC snapshot.

    Attributes:
        channel_map: The exact ChannelMap definition registered with the Flow.
        instance: One logical instance key, or ``None`` to load every instance.
    """

    channel_map: ChannelMap[Any]
    instance: str | None

    @property
    def selector(self) -> str:
        """Return the protocol selector for this typed load.

        Returns:
            ``MapName/`` for all instances, or the escaped physical instance name.
        """
        if self.instance is None:
            return f"{self.channel_map.name}/"
        return f"{self.channel_map.name}/{quote(self.instance, safe='')}"


@dataclass(frozen=True)
class ChannelMessage(Generic[ValueT]):
    """Represent one pending Channel message and its server-assigned identity.

    Attributes:
        message_id: UUIDv7 assigned by Dex when the message was published.
        value: The decoded Channel value.
    """

    message_id: str
    value: ValueT


@dataclass(frozen=True)
class Channel(Generic[ValueT]):
    """Define a typed, durable singleton message stream owned by a Flow.

    Add the Channel to the Flow's ``PersistenceSchema``. Publishers append values;
    Steps use the condition factories to wait for a count and then inspect the
    selected values through ``results``.

    Attributes:
        name: The non-empty Channel name without ``/``, unique within its Flow.
        value_type: The Python type of every published value.

    Examples:
        >>> approvals = Channel("approvals", str)
        >>> wait = Wait.until(approvals.for_n(2, condition_id="two-approvals"))
        >>> approvals.publish(context, "reviewer@example.com")
    """

    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        require_persistence_definition_name(self.name)

    def load_messages(self) -> ChannelLoad:
        """Select this Channel's pending messages for an RPC snapshot.

        Pass the result through ``@rpc(load_channels=(...))``. Loading preserves
        FIFO order and does not consume or lock messages.

        Returns:
            A typed pending-message selection for this Channel.
        """
        return ChannelLoad(self)

    def publish(self, context: Context, value: ValueT) -> None:
        """Stage one value to append with the current handler decision.

        Args:
            context: The current Step or RPC Context.
            value: A value compatible with ``value_type``.
        """
        context._publish_channel(self, None, value)

    def delete(self, context: Context, message_id: str) -> None:
        """Stage deletion of one pending message from an RPC handler.

        Use ``@rpc(is_transactional=True)`` when a missing message must fail the
        entire RPC without committing its other writes. Step Contexts reject this
        operation.

        Args:
            context: The current RPC Context.
            message_id: Non-empty ID returned by a Client pending-message read.
        """
        context._delete_channel_message(self, None, message_id)

    def size(self, context: Context) -> int:
        """Return the current number of queued values.

        Args:
            context: The current Step or RPC Context.

        Returns:
            The non-negative number of queued Channel values.
        """
        return context._channel_size(cast(Channel[object], self), None)

    def pending_messages(self, context: Context) -> Sequence[ChannelMessage[ValueT]]:
        """Return the pending messages loaded for the current RPC snapshot.

        Configure the RPC with ``load_channels=(this_channel.load_messages(),)``.
        The returned sequence preserves FIFO order and does not change after staged
        publications or deletions.

        Args:
            context: The current RPC Context.

        Returns:
            Pending message IDs and decoded values in FIFO order.

        Raises:
            StateNotLoadedError: If the RPC did not load this Channel.
        """
        return cast(
            Sequence[ChannelMessage[ValueT]],
            context._pending_channel_messages(self, None),
        )

    def find_pending_message(
        self, context: Context, message_id: str
    ) -> ChannelMessage[ValueT] | None:
        """Find one loaded pending message by server-assigned ID.

        Args:
            context: The current RPC Context.
            message_id: The message ID to find.

        Returns:
            The matching message, or ``None`` when absent from the snapshot.
        """
        required_id = require_name(message_id)
        return next(
            (
                message
                for message in self.pending_messages(context)
                if message.message_id == required_id
            ),
            None,
        )

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
        """Create a non-blocking condition consuming up to ``count`` queued values.

        The condition does not wait on its own for values to accumulate. When
        its surrounding Wait completes, Dex consumes up to ``count`` values
        queued at that time. An empty queue produces no values.

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
        cannot exceed ``at_most``. Dex waits only for ``at_least``. Once that
        bound is met, it consumes currently queued values up to ``at_most``.
        Omitting ``at_least`` makes the condition complete immediately.

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
    Instance keys must be non-empty and must not contain ``/``.

    Attributes:
        name: The non-empty Channel name without ``/``, unique within its Flow.
        value_type: The Python type of every published value.

    Examples:
        >>> events = ChannelMap("events", dict[str, str])
        >>> events.publish(context, "customer-42", {"kind": "paid"})
        >>> wait = Wait.until(events.for_one("customer-42"))
    """

    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        require_persistence_definition_name(self.name)

    def load_messages(self, instance: str) -> ChannelMapLoad:
        """Select one instance's pending messages for an RPC snapshot.

        Args:
            instance: The non-empty logical ChannelMap instance key.

        Returns:
            A typed selection for this one instance.

        Raises:
            ValueError: If ``instance`` is empty.
        """
        return ChannelMapLoad(self, require_name(instance))

    def load_all_messages(self) -> ChannelMapLoad:
        """Select pending messages from every current map instance.

        Loading preserves each instance's FIFO order and does not consume or lock
        messages. ChannelMap keys and sizes remain available without this load.

        Returns:
            A typed all-instances selection for this ChannelMap.
        """
        return ChannelMapLoad(self, None)

    def publish(self, context: Context, instance: str, value: ValueT) -> None:
        """Stage a value to append to one ChannelMap instance.

        Args:
            context: The current Step or RPC Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.
            value: A value compatible with ``value_type``.
        """
        context._publish_channel(self, instance, value)

    def delete(self, context: Context, instance: str, message_id: str) -> None:
        """Stage deletion of one pending message from a ChannelMap instance.

        Args:
            context: The current RPC Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.
            message_id: Non-empty ID returned by a Client pending-message read.
        """
        context._delete_channel_message(self, instance, message_id)

    def size(self, context: Context, instance: str) -> int:
        """Return the queued value count for one instance.

        Args:
            context: The current Step or RPC Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.

        Returns:
            The non-negative number of queued values for ``instance``.
        """
        return context._channel_size(cast(ChannelMap[object], self), instance)

    def pending_messages(
        self, context: Context, instance: str
    ) -> Sequence[ChannelMessage[ValueT]]:
        """Return one instance's pending messages from the loaded RPC snapshot.

        Configure the RPC with
        ``load_channel_maps=(this_channel_map.load_messages(instance),)`` or use
        ``load_all_messages()``. This method reads one instance.

        Args:
            context: The current RPC Context.
            instance: The non-empty logical map key.

        Returns:
            Pending message IDs and decoded values in FIFO order.

        Raises:
            StateNotLoadedError: If the RPC did not load this ChannelMap.
        """
        return cast(
            Sequence[ChannelMessage[ValueT]],
            context._pending_channel_messages(self, instance),
        )

    def find_pending_message(
        self, context: Context, instance: str, message_id: str
    ) -> ChannelMessage[ValueT] | None:
        """Find one loaded ChannelMap message by server-assigned ID.

        Args:
            context: The current RPC Context.
            instance: The non-empty logical map key.
            message_id: The message ID to find.

        Returns:
            The matching message, or ``None`` when absent from the snapshot.
        """
        required_id = require_name(message_id)
        return next(
            (
                message
                for message in self.pending_messages(context, instance)
                if message.message_id == required_id
            ),
            None,
        )

    def get_map_size(self, context: Context) -> int:
        """Return the number of non-empty instances visible to the current RPC.

        Args:
            context: The current RPC Context.

        Returns:
            The number of keys after including publications buffered by this RPC.
        """
        return len(self.get_all_instance_keys(context))

    def get_all_instance_keys(self, context: Context) -> tuple[str, ...]:
        """Return decoded non-empty instance keys in ascending order.

        Args:
            context: The current RPC Context.

        Returns:
            An immutable tuple including publications buffered by this RPC.
        """
        return context._channel_map_keys(cast(ChannelMap[object], self))

    def results(self, context: Context, instance: str) -> Sequence[ValueT]:
        """Return values selected for one instance by the satisfied condition.

        Args:
            context: The current Step Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.

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
            instance: The map instance. Slash is prohibited because it is a reserved character.
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
            instance: The map instance. Slash is prohibited because it is a reserved character.
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
            instance: The map instance. Slash is prohibited because it is a reserved character.
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
        """Create a non-blocking instance condition consuming up to ``count`` values.

        The condition does not wait on its own for values to accumulate. When
        its surrounding Wait completes, Dex consumes up to ``count`` values
        queued for the instance at that time. An empty queue produces no values.

        Args:
            instance: The map instance. Slash is prohibited because it is a reserved character.
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

        Dex waits only for ``at_least``. Once that bound is met, it consumes
        currently queued values up to ``at_most``. Omitting ``at_least`` makes
        the condition complete immediately.

        Args:
            instance: The map instance. Slash is prohibited because it is a reserved character.
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
