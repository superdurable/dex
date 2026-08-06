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
from datetime import timedelta
from enum import Enum
from typing import Any, Generic, Protocol, Sequence, TypeVar, cast

from dex._contract_utils import require_name, validate_condition_id

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


class Context(Protocol):
    @property
    def flow_id(self) -> str: ...

    @property
    def run_id(self) -> str: ...

    @property
    def step_execution_id(self) -> str: ...

    @property
    def from_step_execution_id(self) -> str: ...

    @property
    def attempt(self) -> int: ...

    def has_timer_fired(self, index: int | None = None) -> bool: ...

    def wait_for_method_failed(self) -> bool: ...

    def set_step_execution_local(self, key: str, value: object) -> None: ...

    def get_step_execution_local(
        self, key: str, value_type: type[ValueT]
    ) -> ValueT | None: ...

    def record_event(self, name: str, value: object) -> None: ...

    def _get_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
    ) -> ValueT: ...

    def _set_attribute(
        self,
        definition: Attribute[ValueT] | AttributeMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None: ...

    def _delete_attribute(
        self,
        definition: Attribute[object] | AttributeMap[object],
        instance: str | None,
    ) -> None: ...

    def _publish_channel(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
        value: ValueT,
    ) -> None: ...

    def _channel_size(
        self,
        definition: Channel[object] | ChannelMap[object],
        instance: str | None,
    ) -> int: ...

    def _channel_results(
        self,
        definition: Channel[ValueT] | ChannelMap[ValueT],
        instance: str | None,
    ) -> Sequence[ValueT]: ...


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
class ConditionCombination:
    conditions: tuple[Condition, ...]

    @staticmethod
    def of(*conditions: Condition) -> ConditionCombination:
        return ConditionCombination(conditions)


class Timer:
    @staticmethod
    def by_duration(
        duration: timedelta,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return TimerCondition(condition_id=condition_id, duration=duration)


@dataclass(frozen=True)
class Channel(Generic[ValueT]):
    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        require_name(self.name)

    def publish(self, context: Context, value: ValueT) -> None:
        context._publish_channel(self, None, value)

    def size(self, context: Context) -> int:
        return context._channel_size(cast(Channel[object], self), None)

    def results(self, context: Context) -> Sequence[ValueT]:
        return context._channel_results(self, None)

    def for_one(self, *, condition_id: str | None = None) -> Condition:
        return self.for_range(at_least=1, at_most=1, condition_id=condition_id)

    def for_n(self, count: int, *, condition_id: str | None = None) -> Condition:
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
        return self.for_range(at_least=count, condition_id=condition_id)

    def at_most(
        self,
        count: int,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return self.for_range(at_most=count, condition_id=condition_id)

    def for_range(
        self,
        *,
        at_least: int | None = None,
        at_most: int | None = None,
        condition_id: str | None = None,
    ) -> Condition:
        return ChannelCondition(
            condition_id=condition_id,
            channel=self,
            at_least=at_least,
            at_most=at_most,
        )


@dataclass(frozen=True)
class ChannelMap(Generic[ValueT]):
    name: str
    value_type: type[ValueT]

    def __post_init__(self) -> None:
        require_name(self.name)

    def publish(self, context: Context, instance: str, value: ValueT) -> None:
        context._publish_channel(self, instance, value)

    def size(self, context: Context, instance: str) -> int:
        return context._channel_size(cast(ChannelMap[object], self), instance)

    def results(self, context: Context, instance: str) -> Sequence[ValueT]:
        return context._channel_results(self, instance)

    def for_one(
        self,
        instance: str,
        *,
        condition_id: str | None = None,
    ) -> Condition:
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
        return ChannelCondition(
            condition_id=condition_id,
            channel=self,
            instance=instance,
            at_least=at_least,
            at_most=at_most,
        )


class WaitKind(Enum):
    SKIP_IMMEDIATELY = "skip_immediately"
    ALL_OF = "all_of"
    ANY_OF = "any_of"
    ANY_COMBINATION_OF = "any_combination_of"


@dataclass(frozen=True)
class Wait:
    kind: WaitKind
    conditions: tuple[Condition, ...] = ()
    combinations: tuple[ConditionCombination, ...] = ()

    @staticmethod
    def skip_immediately() -> Wait:
        return Wait(WaitKind.SKIP_IMMEDIATELY)

    @staticmethod
    def all_of(*conditions: Condition) -> Wait:
        return Wait(WaitKind.ALL_OF, conditions)

    @staticmethod
    def any_of(*conditions: Condition) -> Wait:
        return Wait(WaitKind.ANY_OF, conditions)

    @staticmethod
    def any_combination_of(*combinations: ConditionCombination) -> Wait:
        return Wait(
            WaitKind.ANY_COMBINATION_OF,
            combinations=combinations,
        )
