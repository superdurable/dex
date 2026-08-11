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
    """Describe the durable conditions evaluated before a Step executes.

    A Step returns a Wait from ``wait_for``. Dex persists the condition tree and
    invokes ``execute`` only when the selected readiness rule succeeds.

    Attributes:
        kind: The combination rule applied by Dex.
        conditions: Direct Conditions for all-of or any-of waits.
        combinations: Alternative all-of groups for any-combination waits.

    Examples:
        >>> wait = Wait.any_of(
        ...     replies.for_one(condition_id="reply"),
        ...     Timer.by_duration(timedelta(hours=1), condition_id="timeout"),
        ... )
    """

    kind: WaitKind
    conditions: tuple[Condition, ...] = ()
    combinations: tuple[ConditionCombination, ...] = ()

    @staticmethod
    def skip_immediately() -> Wait:
        """Create a Wait that proceeds to ``execute`` immediately.

        Returns:
            A Wait with no Conditions.
        """
        return Wait(WaitKind.SKIP_IMMEDIATELY)

    @staticmethod
    def until(condition: Condition) -> Wait:
        """Create a Wait for one Condition.

        Args:
            condition: The only readiness condition.

        Returns:
            An all-of Wait with ``condition``.
        """
        return Wait.all_of(condition)

    @staticmethod
    def all_of(*conditions: Condition) -> Wait:
        """Create a Wait that requires every Condition.

        Args:
            *conditions: Conditions evaluated as one all-of group.

        Returns:
            An all-of Wait with Conditions in argument order.
        """
        return Wait(WaitKind.ALL_OF, conditions)

    @staticmethod
    def any_of(*conditions: Condition) -> Wait:
        """Create a Wait that continues when any Condition is ready.

        Args:
            *conditions: Alternative readiness Conditions.

        Returns:
            An any-of Wait with Conditions in argument order.
        """
        return Wait(WaitKind.ANY_OF, conditions)

    @staticmethod
    def any_combination_of(*combinations: ConditionCombination) -> Wait:
        """Create a Wait that accepts any complete Condition combination.

        Args:
            *combinations: Alternative all-of groups created with
                :meth:`ConditionCombination.of`.

        Returns:
            A Wait containing the alternative groups.
        """
        return Wait(
            WaitKind.ANY_COMBINATION_OF,
            combinations=combinations,
        )
