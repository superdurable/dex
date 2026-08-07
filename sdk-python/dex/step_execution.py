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

from dex._utils import require_name


@dataclass(frozen=True)
class StepExecutionId:
    step_type: str
    number: int = 1


@dataclass(frozen=True)
class TimerId:
    condition_id: str | None = None
    condition_index: int | None = None

    def __post_init__(self) -> None:
        if (self.condition_id is None) == (self.condition_index is None):
            raise ValueError("TimerId requires exactly one selector")
        if self.condition_index is not None and self.condition_index < 0:
            raise ValueError("timer condition index must not be negative")

    @staticmethod
    def by_condition_id(condition_id: str) -> TimerId:
        require_name(condition_id)
        return TimerId(condition_id=condition_id)

    @staticmethod
    def by_condition_index(condition_index: int) -> TimerId:
        return TimerId(condition_index=condition_index)
