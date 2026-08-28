// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::time::Duration;

use std::sync::LazyLock;

use dex_sdk::{
    Channel, Context, Flow, HandlerResult, PersistenceSchema, Step, StepDecision, StepList,
    StepMovement, Timer, Wait,
};
use serde::{Deserialize, Serialize};

#[derive(Default)]
pub struct CronScheduleFlow {
    start: Start,
    wait_for_schedule: WaitForSchedule,
    run: Run,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub enum IntervalUnit {
    Minute,
    Hour,
    Day,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Interval {
    pub value: u64,
    pub unit: IntervalUnit,
}

impl Interval {
    fn duration(&self) -> Duration {
        match self.unit {
            IntervalUnit::Minute => Duration::from_secs(self.value * 60),
            IntervalUnit::Hour => Duration::from_secs(self.value * 3_600),
            IntervalUnit::Day => Duration::from_secs(self.value * 86_400),
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct CronScheduleInput {
    pub interval: Interval,
    pub run_count: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct ScheduleState {
    interval: Interval,
    remaining_runs: u32,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
struct RunInput {
    run_number: u32,
    is_final: bool,
}

impl Flow for CronScheduleFlow {
    type StartInput = CronScheduleInput;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
            .and(&self.wait_for_schedule)
            .and(&self.run)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&TRIGGER).channel(&SKIP)
    }
}

#[derive(Default)]
struct Start;

impl Step for Start {
    type Input = CronScheduleInput;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if input.run_count == 0 || input.interval.value == 0 {
            return Ok(StepDecision::force_fail(
                "interval value and run count must be positive",
            ));
        }
        Ok(StepDecision::go_to(
            &WaitForSchedule,
            ScheduleState {
                interval: input.interval,
                remaining_runs: input.run_count,
            },
        ))
    }
}

#[derive(Default)]
struct WaitForSchedule;

impl Step for WaitForSchedule {
    type Input = ScheduleState;

    fn wait_for(&self, _context: &mut Context, state: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            Timer::by_duration(state.interval.duration()),
            TRIGGER.for_one(),
            SKIP.for_one(),
        ]))
    }

    fn execute(&self, context: &mut Context, state: Self::Input) -> HandlerResult<StepDecision> {
        if !SKIP.condition_results(context)?.is_empty() {
            return Ok(next_schedule(state));
        }
        let run_input = RunInput {
            run_number: state.remaining_runs,
            is_final: state.remaining_runs == 1,
        };
        if run_input.is_final {
            return Ok(StepDecision::go_to(&Run, run_input));
        }
        Ok(StepDecision::go_to_many([
            StepMovement::to(&Run, run_input),
            StepMovement::to(
                &WaitForSchedule,
                ScheduleState {
                    interval: state.interval,
                    remaining_runs: state.remaining_runs - 1,
                },
            ),
        ]))
    }
}

#[derive(Default)]
struct Run;

impl Step for Run {
    type Input = RunInput;

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        context.record_event("cron-schedule-run", format!("run-{}", input.run_number))?;
        Ok(if input.is_final {
            StepDecision::graceful_complete(())
        } else {
            StepDecision::dead_end()
        })
    }
}

fn next_schedule(state: ScheduleState) -> StepDecision {
    if state.remaining_runs == 1 {
        return StepDecision::graceful_complete(());
    }
    StepDecision::go_to(
        &WaitForSchedule,
        ScheduleState {
            interval: state.interval,
            remaining_runs: state.remaining_runs - 1,
        },
    )
}

pub static TRIGGER: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("cron-schedule-trigger"));

pub static SKIP: LazyLock<Channel<()>> = LazyLock::new(|| Channel::new("cron-schedule-skip"));
