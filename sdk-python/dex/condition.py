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
from typing import TYPE_CHECKING, Generic, TypeVar

from dex._utils import validate_condition_id

if TYPE_CHECKING:
    from dex.channel import Channel, ChannelMap

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
