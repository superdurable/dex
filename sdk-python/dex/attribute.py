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

from dex._utils import require_name
from dex.context import Context

ValueT = TypeVar("ValueT")


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

    Attributes:
        name: The non-empty logical Attribute name, unique within its Flow.
        value_type: The Python type used to encode and decode values.
        index: Optional search-index configuration; ``None`` disables indexing.

    Examples:
        >>> status = Attribute("status", str, AttributeIndex(IndexType.KEYWORD))
        >>> status.set(context, "paid")
        >>> status.get(context)
        'paid'
    """

    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None

    def __post_init__(self) -> None:
        require_name(self.name)

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
            An immutable lock descriptor for this Attribute.
        """
        return AttributeLock(self)


@dataclass(frozen=True)
class AttributeMap(Generic[ValueT]):
    """Define a typed family of durable values keyed by map instance.

    AttributeMap instances share one schema definition while retaining independent
    values and locks. Declare the map in ``PersistenceSchema`` before using it.

    Attributes:
        name: The non-empty logical Attribute name, unique within its Flow.
        value_type: The Python type used for every map instance.
        index: Optional shared search-index configuration.

    Examples:
        >>> balances = AttributeMap("balance", int)
        >>> balances.set(context, "merchant-7", 1200)
        >>> balances.get(context, "merchant-7")
        1200
    """

    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None

    def __post_init__(self) -> None:
        require_name(self.name)

    def get(self, context: Context, instance: str) -> ValueT:
        """Return one map instance from a Step or RPC Context.

        Args:
            context: The current handler Context.
            instance: The non-empty logical map key.

        Returns:
            The decoded instance value.

        Raises:
            KeyError: If the instance has no value.
            ValueError: If ``instance`` is empty.
        """
        return context._get_attribute(self, instance)

    def set(self, context: Context, instance: str, value: ValueT) -> None:
        """Stage one map-instance value for the current handler decision.

        Args:
            context: The current handler Context.
            instance: The non-empty logical map key.
            value: A value compatible with ``value_type``.
        """
        context._set_attribute(self, instance, value)

    def delete(self, context: Context, instance: str) -> None:
        """Stage deletion of one map instance.

        Args:
            context: The current handler Context.
            instance: The non-empty logical map key.
        """
        context._delete_attribute(cast(AttributeMap[object], self), instance)

    def lock(self, instance: str) -> AttributeLock:
        """Return a lock request for one map instance.

        Args:
            instance: The non-empty logical map key.

        Returns:
            An immutable lock descriptor scoped to ``instance``.

        Raises:
            ValueError: If ``instance`` is empty.
        """
        require_name(instance)
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
