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
    """Identify one numbered execution of a Step type.

    Attributes:
        step_type: The registered Step type name.
        number: The positive execution number; defaults to the first execution.
    """

    step_type: str
    number: int = 1


@dataclass(frozen=True)
class TimerId:
    """Select a Timer condition within a Step execution.

    Exactly one selector must be set. Prefer ``condition_id`` for definitions that
    can evolve; ``condition_index`` is zero-based and follows flattened Wait order.

    Attributes:
        condition_id: A stable Timer condition identifier, or ``None``.
        condition_index: A zero-based Timer condition index, or ``None``.
    """

    condition_id: str | None = None
    condition_index: int | None = None

    def __post_init__(self) -> None:
        if (self.condition_id is None) == (self.condition_index is None):
            raise ValueError("TimerId requires exactly one selector")
        if self.condition_index is not None and self.condition_index < 0:
            raise ValueError("timer condition index must not be negative")

    @staticmethod
    def by_condition_id(condition_id: str) -> TimerId:
        """Select a Timer by its stable condition ID.

        Args:
            condition_id: The non-empty ID assigned by ``Timer.by_duration``.

        Returns:
            A TimerId using the ID selector.
        """
        require_name(condition_id)
        return TimerId(condition_id=condition_id)

    @staticmethod
    def by_condition_index(condition_index: int) -> TimerId:
        """Select a Timer by zero-based Wait-tree position.

        Args:
            condition_index: The non-negative flattened Timer index.

        Returns:
            A TimerId using the index selector.

        Raises:
            ValueError: If ``condition_index`` is negative.
        """
        return TimerId(condition_index=condition_index)
