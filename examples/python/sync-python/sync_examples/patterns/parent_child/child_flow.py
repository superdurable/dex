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

import random
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
    graceful_complete,
)


class ChildProcessing(Step[str]):
    def wait_for(self, context: Context, input: str) -> Wait:
        return Wait.until(Timer.by_duration(timedelta(seconds=random.randint(1, 3))))

    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete()


class ChildFlow(Flow[str]):
    def __init__(self) -> None:
        self.processing = ChildProcessing()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.processing)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
