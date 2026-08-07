# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from datetime import timedelta

from dex.condition import Condition, TimerCondition


class Timer:
    @staticmethod
    def by_duration(
        duration: timedelta,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return TimerCondition(condition_id=condition_id, duration=duration)
