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

/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

use std::time::Duration;

use dex_sdk::{
    Context, Flow, FlowTimeoutHandler, HandlerResult, Step, StepDecision, StepList, Timer, Wait,
};

#[derive(Default)]
pub struct FlowGracefulTimeout {
    long_wait_step: LongWaitStep,
}

impl Flow for FlowGracefulTimeout {
    type StartInput = bool;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.long_wait_step)
    }

    fn timeout_handler(&self) -> Option<FlowTimeoutHandler<Self>> {
        Some(Self::handle_timeout)
    }
}

#[derive(Default)]
struct LongWaitStep;

impl Step for LongWaitStep {
    type Input = bool;

    fn wait_for(&self, _context: &mut Context, successful: bool) -> HandlerResult<Wait> {
        let duration = if successful { 1 } else { 65 };
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            duration,
        ))))
    }

    fn execute(&self, _context: &mut Context, _successful: bool) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_complete("task-completed".to_string()))
    }
}

impl FlowGracefulTimeout {
    fn handle_timeout(&self, _context: &mut Context) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("task exceeded graceful timeout"))
    }
}
