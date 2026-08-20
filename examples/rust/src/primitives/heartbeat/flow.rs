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

use std::thread;
use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerResult, RetryPolicy, Step, StepDecision, StepList, StepOptions,
};

#[derive(Default)]
pub struct HeartbeatFlow {
    start: HeartbeatStep,
}

impl Flow for HeartbeatFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

#[derive(Default)]
struct HeartbeatStep;

impl Step for HeartbeatStep {
    type Input = i32;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_method_timeout(Duration::from_secs(60))
            .heartbeat_timeout(Duration::from_secs(10))
            .execute_retry(RetryPolicy::new().maximum_attempts(3))
    }

    fn execute(&self, context: &mut Context, batches: Self::Input) -> HandlerResult<StepDecision> {
        for _batch in 0..batches {
            if context.is_cancelled() {
                return Ok(StepDecision::dead_end());
            }
            thread::sleep(Duration::from_secs(2));
        }
        Ok(StepDecision::graceful_complete(String::from("processed")))
    }
}
