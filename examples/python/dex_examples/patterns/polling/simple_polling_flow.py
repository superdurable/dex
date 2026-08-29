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

from datetime import timedelta

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    go_to,
    graceful_complete,
)

POLLING_INTERVAL = timedelta(seconds=10)


class PollingStep(Step[None]):

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(Timer.by_duration(POLLING_INTERVAL))

    def execute(self, context: Context, input: None) -> StepDecision:
        if self._is_system_ready():
            return graceful_complete()
        return go_to(PollingStep, None)

    @staticmethod
    def _is_system_ready() -> bool:
        print("Executing external system check for readiness...")
        return True


class PollingWithTimerFlow(Flow[None]):
    def __init__(self) -> None:
        self.polling_step = PollingStep()

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.polling_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
