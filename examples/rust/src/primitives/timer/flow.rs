// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::time::Duration;

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList, Timer, Wait};

#[derive(Default)]
pub struct TimerFlow {
    start: TimerStep,
}

impl Flow for TimerFlow {
    type StartInput = i32;

    fn flow_type(&self) -> &'static str {
        "TimerFlow"
    }

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

#[derive(Default)]
struct TimerStep;

impl Step for TimerStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            input.max(0) as u64,
        ))))
    }

    fn execute(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("timer-fired".to_string()))
    }
}
