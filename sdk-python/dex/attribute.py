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

from dex._contract_utils import require_name
from dex.context import Context

ValueT = TypeVar("ValueT")


class IndexType(Enum):
    KEYWORD = "keyword"
    FULL_TEXT = "full_text"
    KEYWORD_ARRAY = "keyword_array"
    INT = "int"
    DOUBLE = "double"
    BOOL = "bool"
    DATETIME = "datetime"


@dataclass(frozen=True)
class AttributeIndex:
    type: IndexType
    index_key: str = ""


@dataclass(frozen=True)
class Attribute(Generic[ValueT]):
    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None

    def __post_init__(self) -> None:
        require_name(self.name)

    def get(self, context: Context) -> ValueT:
        return context._get_attribute(self, None)

    def set(self, context: Context, value: ValueT) -> None:
        context._set_attribute(self, None, value)

    def delete(self, context: Context) -> None:
        context._delete_attribute(cast(Attribute[object], self), None)

    def lock(self) -> AttributeLock:
        return AttributeLock(self)


@dataclass(frozen=True)
class AttributeMap(Generic[ValueT]):
    name: str
    value_type: type[ValueT]
    index: AttributeIndex | None = None

    def __post_init__(self) -> None:
        require_name(self.name)

    def get(self, context: Context, instance: str) -> ValueT:
        return context._get_attribute(self, instance)

    def set(self, context: Context, instance: str, value: ValueT) -> None:
        context._set_attribute(self, instance, value)

    def delete(self, context: Context, instance: str) -> None:
        context._delete_attribute(cast(AttributeMap[object], self), instance)

    def lock(self, instance: str) -> AttributeLock:
        require_name(instance)
        return AttributeLock(self, instance)


@dataclass(frozen=True)
class AttributeLock:
    attribute: Attribute[Any] | AttributeMap[Any]
    instance: str | None = None
