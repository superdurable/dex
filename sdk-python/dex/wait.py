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
        ...     replies.for_one(),
        ...     Timer.by_duration(timedelta(hours=1)),
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

        The Condition does not need a condition ID.

        Args:
            condition: The only readiness condition.

        Returns:
            An all-of Wait with ``condition``.
        """
        return Wait.all_of(condition)

    @staticmethod
    def all_of(*conditions: Condition) -> Wait:
        """Create a Wait that requires every Condition.

        This combinator does not require condition IDs. Every Condition in
        :meth:`any_combination_of` does require one.

        Args:
            *conditions: Conditions evaluated as one all-of group.

        Returns:
            An all-of Wait with Conditions in argument order.
        """
        return Wait(WaitKind.ALL_OF, conditions)

    @staticmethod
    def any_of(*conditions: Condition) -> Wait:
        """Create a Wait that continues when any Condition is ready.

        Conditions do not need condition IDs. Read Channel results from the
        Channel definition and inspect Timer outcomes through ``Context``.

        Channel consumption is not greedy across alternatives. Dex consumes
        messages only from the selected Channel Condition; other ready Channel
        Conditions consume nothing.

        Args:
            *conditions: Alternative readiness Conditions.

        Returns:
            An any-of Wait with Conditions in argument order.
        """
        return Wait(WaitKind.ANY_OF, conditions)

    @staticmethod
    def any_combination_of(*combinations: ConditionCombination) -> Wait:
        """Create a Wait that accepts any complete Condition combination.

        Channel consumption is not greedy across combinations. Dex consumes
        messages only from Channel Conditions in the selected combination;
        Conditions belonging only to other ready combinations consume nothing.

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
