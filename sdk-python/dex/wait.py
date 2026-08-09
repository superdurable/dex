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

from dex.condition import Condition, ConditionCombination


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
    def until(condition: Condition) -> Wait:
        return Wait.all_of(condition)

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
