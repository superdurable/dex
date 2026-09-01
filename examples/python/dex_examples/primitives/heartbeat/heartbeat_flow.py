# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

import asyncio
from datetime import timedelta

from dex import (
    AsyncContext,
    Flow,
    RetryPolicy,
    Step,
    StepDecision,
    StepList,
    StepOptions,
    dead_end,
    graceful_complete,
)


class HeartbeatStep(Step[int]):
    def get_step_options(self) -> StepOptions:
        return StepOptions(
            execute_method_timeout=timedelta(seconds=60),
            heartbeat_timeout=timedelta(seconds=10),
            execute_retry=RetryPolicy(maximum_attempts=3),
        )

    async def execute(
        self, context: AsyncContext, batches: int
    ) -> StepDecision:
        completed_batches = context.get_last_heartbeat_value(int) or 0
        for batch in range(completed_batches, batches):
            if context.is_cancellation_requested():
                return dead_end()
            await asyncio.sleep(2)
            await context.heartbeat(batch + 1)
        return graceful_complete("processed")


class HeartbeatFlow(Flow[int]):
    def __init__(self) -> None:
        self.start = HeartbeatStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
