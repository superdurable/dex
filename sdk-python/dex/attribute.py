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
from enum import Enum
from typing import Any, Generic, TypeVar, cast
from urllib.parse import quote

from dex._utils import require_map_instance, require_persistence_definition_name
from dex.context import Context
from dex.dexpb import dex_pb2 as pb

ValueT = TypeVar("ValueT")


@dataclass(frozen=True)
class AttributeMapLoad:
    """Select one AttributeMap instance for an RPC snapshot.

    Create a selection with :meth:`AttributeMap.load`. Loading provides a point-in-time
    value snapshot; it does not lock the map against concurrent writers.

    Attributes:
        attribute_map: The exact AttributeMap definition registered with the Flow.
        instance: One logical instance key.
    """

    attribute_map: AttributeMap[Any]
    instance: str

    @property
    def selector(self) -> str:
        """Return the protocol selector for this typed load.

        Returns:
            The encoded physical instance name.
        """
        if "/" in self.instance:
            raise ValueError("map instances must not contain '/'")
        return f"{self.attribute_map.name}/{quote(self.instance, safe='')}"


class IndexType(Enum):
    """Select how Dex indexes an Attribute for Flow search.

    Attributes:
        KEYWORD: Index an exact string value.
        FULL_TEXT: Index a string for tokenized full-text matching.
        KEYWORD_ARRAY: Index every string in a sequence as a keyword.
        INT: Index a signed 64-bit integer.
        DOUBLE: Index a finite floating-point number.
        BOOL: Index a Boolean value.
        DATETIME: Index a :class:`datetime.datetime` value.
    """

    KEYWORD = "keyword"
    FULL_TEXT = "full_text"
    KEYWORD_ARRAY = "keyword_array"
    INT = "int"
    DOUBLE = "double"
    BOOL = "bool"
    DATETIME = "datetime"


@dataclass(frozen=True)
class AttributeIndex:
    """Configure the search index for an Attribute or AttributeMap.

    Attributes:
        type: The value representation accepted by the search index.
        index_key: The physical search key. An empty value uses the Attribute name
            for a singleton Attribute; AttributeMap definitions require an explicit
            key so all instances share one index.
    """

    type: IndexType
    index_key: str = ""


@dataclass(frozen=True)
class Attribute(Generic[ValueT]):
    """Define a typed, durable singleton value owned by a Flow.

    Declare Attributes once in a Flow's ``PersistenceSchema`` and reuse the same
    definition from Steps, RPCs, and Client calls. Values are read and written
    through a handler :class:`Context`; Client methods provide external access.

    Set ``sync_to_attribute_store`` to project every write asynchronously through
    the Flow's configured Attribute Store. Projection is latest-state only, deletion
    projects SQL NULL, and failures do not roll back the Flow Attribute.

    Attributes:
        name: The non-empty logical Attribute name without ``/``, unique within its Flow.
        value_type: The Python type used to encode and decode values.
        index: Optional search-index configuration; ``None`` disables indexing.
        sync_to_attribute_store: Whether writes are projected to the selected
            Attribute Store. Defaults to ``False``.

    Examples:
        >>> status = Attribute("status", str, sync_to_attribute_store=True)
        >>> status.set(context, "paid")
        >>> status.get(context)
        'paid'
    """

    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None
    sync_to_attribute_store: bool = False

    def __post_init__(self) -> None:
        require_persistence_definition_name(self.name)

    def get(self, context: Context) -> ValueT:
        """Return the current value from a Step or RPC Context.

        Args:
            context: The current handler Context.

        Returns:
            The decoded value.

        Raises:
            KeyError: If the Attribute has no value.
            TypeError: If the stored value cannot be decoded as ``value_type``.
        """
        return context._get_attribute(self, None)

    def set(self, context: Context, value: ValueT) -> None:
        """Stage a value to persist with the current handler decision.

        Args:
            context: The current handler Context.
            value: A value compatible with ``value_type``.

        Raises:
            TypeError: If ``value`` cannot be encoded as ``value_type``.
        """
        context._set_attribute(self, None, value)

    def delete(self, context: Context) -> None:
        """Stage deletion of this Attribute in the current handler decision.

        Args:
            context: The current handler Context.
        """
        context._delete_attribute(cast(Attribute[object], self), None)

    def lock(self) -> AttributeLock:
        """Return a lock request for this Attribute.

        Use the result in Step or RPC lock options to serialize handlers that
        access the same value.

        Returns:
            A lock for this Attribute.
        """
        return AttributeLock(self)


@dataclass(frozen=True)
class AttributeMap(Generic[ValueT]):
    """Define a typed family of durable values keyed by map instance.

    AttributeMap instances share one schema definition while keeping independent
    values and locks. Declare the map in ``PersistenceSchema`` before using it.
    Instance keys must be non-empty and must not contain ``/``.
    Synced instances use their physical Attribute names as target columns. Projection
    is asynchronous and latest-state only, deletion projects SQL NULL, and failures
    do not roll back Flow Attributes.

    Attributes:
        name: The non-empty logical Attribute name without ``/``, unique within its Flow.
        value_type: The Python type used for every map instance.
        index: Optional shared search-index configuration.
        sync_to_attribute_store: Whether every instance is projected to the selected
            Attribute Store. Defaults to ``False``.

    Examples:
        >>> balances = AttributeMap("balance", int, sync_to_attribute_store=True)
        >>> balances.set(context, "merchant-7", 1200)
        >>> balances.get(context, "merchant-7")
        1200
    """

    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None
    sync_to_attribute_store: bool = False

    def __post_init__(self) -> None:
        require_persistence_definition_name(self.name)

    def load(self, instance: str) -> AttributeMapLoad:
        """Select one instance for an RPC snapshot.

        Pass the result through ``@rpc(load_attribute_map_instances=(...))``.

        Args:
            instance: The non-empty, slash-free logical map key to load.

        Returns:
            A typed selection for this one instance.

        Raises:
            ValueError: If ``instance`` is empty or contains ``/``.
        """
        return AttributeMapLoad(self, require_name(instance))

    def get(self, context: Context, instance: str) -> ValueT:
        """Return one map instance from a Step or RPC Context.

        Args:
            context: The current handler Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.

        Returns:
            The decoded instance value.

        Raises:
            KeyError: If the instance has no value.
            ValueError: If ``instance`` is empty or contains ``/``.
        """
        return context._get_attribute(self, instance)

    def set(self, context: Context, instance: str, value: ValueT) -> None:
        """Stage one map-instance value for the current handler decision.

        Args:
            context: The current handler Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.
            value: A value compatible with ``value_type``.
        """
        context._set_attribute(self, instance, value)

    def delete(self, context: Context, instance: str) -> None:
        """Stage deletion of one map instance.

        Args:
            context: The current handler Context.
            instance: The map instance. Slash is prohibited because it is a reserved character.
        """
        context._delete_attribute(cast(AttributeMap[object], self), instance)

    def get_map_size(self, context: Context) -> int:
        """Return the number of existing instances, including buffered writes.

        Args:
            context: The current Step or RPC Context.

        Returns:
            The number of keys visible after decision-local writes and deletions.
        """
        return len(self.get_all_instance_keys(context))

    def get_all_instance_keys(self, context: Context) -> tuple[str, ...]:
        """Return decoded existing instance keys in ascending order.

        Args:
            context: The current Step or RPC Context.

        Returns:
            An immutable tuple reflecting decision-local writes and deletions.
        """
        return context._attribute_map_keys(cast(AttributeMap[object], self))

    def lock(self, instance: str) -> AttributeLock:
        """Return a lock request for one map instance.

        Args:
            instance: The map instance. Slash is prohibited because it is a reserved character.

        Returns:
            A lock for ``instance``.

        Raises:
            ValueError: If ``instance`` is empty or contains ``/``.
        """
        require_map_instance(instance)
        return AttributeLock(self, instance)


@dataclass(frozen=True)
class AttributeLock:
    """Describe an Attribute lock acquired for a Step or RPC handler.

    Attributes:
        attribute: The singleton Attribute or AttributeMap definition to lock.
        instance: The AttributeMap instance, or ``None`` for a singleton Attribute.
    """

    attribute: Attribute[Any] | AttributeMap[Any]
    instance: str | None = None

    def __post_init__(self) -> None:
        if self.instance is not None:
            require_map_instance(self.instance)


def _apply_attribute_store_sync(
    write: pb.AttributeWrite,
    definition: Attribute[Any] | AttributeMap[Any],
) -> None:
    if definition.sync_to_attribute_store:
        write.sync_config.enabled = True
