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
