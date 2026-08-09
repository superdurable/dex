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


class SimplePollingComplete(Step[None]):
    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        print("Executing final state to complete the workflow...")
        return graceful_complete()


class SimplePolling(Step[None]):
    def __init__(self, simple_polling_complete: SimplePollingComplete) -> None:
        self.simple_polling_complete = simple_polling_complete

    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(Timer.by_duration(POLLING_INTERVAL))

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        if self._is_system_ready():
            return go_to(self.simple_polling_complete, None)
        return go_to(self, None)

    @staticmethod
    def _is_system_ready() -> bool:
        print("Executing external system check for readiness...")
        return True


class SimplePollingFlow(Flow[None]):
    def __init__(self) -> None:
        self.simple_polling_complete = SimplePollingComplete()
        self.simple_polling = SimplePolling(self.simple_polling_complete)

    def get_steps(self) -> StepList[None]:
        return StepList.start_step(self.simple_polling).other_steps(
            self.simple_polling_complete
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
