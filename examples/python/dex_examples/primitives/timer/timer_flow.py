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

"""Minimal Timer Flow: wait until a duration elapses."""

from __future__ import annotations

from datetime import timedelta

from dex import (
    Context,
    Flow,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    graceful_complete,
)


class TimerStep(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(Timer.by_duration(timedelta(seconds=input)))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete("timer-fired")


class TimerFlow(Flow[int]):
    start = TimerStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.start)
