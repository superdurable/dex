# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

from dataclasses import dataclass
from datetime import timedelta
from enum import StrEnum

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Timer,
    Wait,
    dead_end,
    force_fail,
    go_to,
    go_to_multi,
    graceful_complete,
)

CRON_SCHEDULE_FLOW_ID = "cron-schedule-sample"


class IntervalUnit(StrEnum):
    MINUTE = "minute"
    HOUR = "hour"
    DAY = "day"


@dataclass(frozen=True)
class Interval:
    value: int
    unit: IntervalUnit

    def duration(self) -> timedelta:
        match self.unit:
            case IntervalUnit.MINUTE:
                return timedelta(minutes=self.value)
            case IntervalUnit.HOUR:
                return timedelta(hours=self.value)
            case IntervalUnit.DAY:
                return timedelta(days=self.value)


@dataclass(frozen=True)
class CronScheduleInput:
    interval: Interval
    run_count: int


@dataclass(frozen=True)
class _ScheduleState:
    interval: Interval
    remaining_runs: int


@dataclass(frozen=True)
class _RunInput:
    run_number: int
    is_final: bool


class _Start(Step[CronScheduleInput]):
    def execute(
        self, context: Context, input: CronScheduleInput
    ) -> StepDecision:
        del context
        if input.run_count <= 0 or input.interval.value <= 0:
            return force_fail("interval value and run count must be positive")
        return go_to(_WaitForSchedule, _ScheduleState(input.interval, input.run_count))


class _WaitForSchedule(Step[_ScheduleState]):
    def __init__(
        self,
        trigger: Channel[None],
        skip: Channel[None],
    ) -> None:
        self.trigger = trigger
        self.skip = skip

    def wait_for(self, context: Context, state: _ScheduleState) -> Wait:
        del context
        return Wait.any_of(
            Timer.by_duration(state.interval.duration()),
            self.trigger.for_one(),
            self.skip.for_one(),
        )

    def execute(self, context: Context, state: _ScheduleState) -> StepDecision:
        if self.skip.results(context):
            return self._next_schedule(state)
        return self._run_now(state)

    def _next_schedule(self, state: _ScheduleState) -> StepDecision:
        if state.remaining_runs == 1:
            return graceful_complete()
        return go_to(
            self,
            _ScheduleState(state.interval, state.remaining_runs - 1),
        )

    def _run_now(self, state: _ScheduleState) -> StepDecision:
        run_input = _RunInput(
            run_number=state.remaining_runs,
            is_final=state.remaining_runs == 1,
        )
        if run_input.is_final:
            return go_to(_Run, run_input)
        return go_to_multi(
            StepMovement.of(_Run, run_input),
            StepMovement.of(
                _WaitForSchedule,
                _ScheduleState(state.interval, state.remaining_runs - 1),
            ),
        )


class _Run(Step[_RunInput]):
    def execute(self, context: Context, input: _RunInput) -> StepDecision:
        context.record_event("cron-schedule-run", f"run-{input.run_number}")
        return graceful_complete() if input.is_final else dead_end()


class CronScheduleFlow(Flow[CronScheduleInput]):
    def __init__(self) -> None:
        self.trigger = Channel[None]("cron-schedule-trigger", type(None))
        self.skip = Channel[None]("cron-schedule-skip", type(None))
        self.run = _Run()
        self.schedule = _WaitForSchedule(self.trigger, self.skip)
        self.start = _Start()

    def get_steps(self) -> StepList[CronScheduleInput]:
        return StepList.start_step(self.start).other_steps(self.schedule, self.run)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.trigger, self.skip)
