# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Super Durable Source License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

from datetime import timedelta

from dex.condition import Condition, TimerCondition


class Timer:
    """Create durable Timer conditions for Step waits.

    Timer conditions measure durable workflow time rather than blocking a Python
    worker thread. Use a stable ``condition_id`` when external code may skip the
    Timer by ID.
    """

    @staticmethod
    def by_duration(
        duration: timedelta,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        """Create a Timer that becomes ready after ``duration``.

        Args:
            duration: A non-negative delay measured from the wait's start.
            condition_id: Optional stable identifier unique within the Wait tree.

        Returns:
            A durable Timer condition accepted by :class:`Wait`.

        Raises:
            ValueError: If ``duration`` is negative or the identifier is invalid.

        Examples:
            >>> reminder = Timer.by_duration(timedelta(minutes=15), condition_id="remind")
            >>> wait = Wait.until(reminder)
        """
        return TimerCondition(condition_id=condition_id, duration=duration)
