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

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
)

CRON_SCHEDULE_FLOW_ID = "cron-schedule-sample"
CRON_SCHEDULE_EXPRESSION = "*/1 * * * *"


class CronScheduleStep(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return graceful_complete()


class CronScheduleFlow(Flow[None]):
    def __init__(self) -> None:
        self.cron_schedule_step = CronScheduleStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.cron_schedule_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
