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
    @staticmethod
    def by_duration(
        duration: timedelta,
        *,
        condition_id: str | None = None,
    ) -> Condition:
        return TimerCondition(condition_id=condition_id, duration=duration)
