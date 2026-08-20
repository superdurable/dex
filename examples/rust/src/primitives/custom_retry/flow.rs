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

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, RetryPolicy, Step, StepDecision, StepList,
    StepOptions,
};

#[derive(Default)]
pub struct CustomRetryFlow {
    start: CustomRetryStep,
}

impl Flow for CustomRetryFlow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

#[derive(Default)]
struct CustomRetryStep;

impl Step for CustomRetryStep {
    type Input = i32;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(5))
    }

    fn execute(
        &self,
        context: &mut Context,
        ready_after_attempt: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if i32::try_from(context.attempt()).unwrap_or(i32::MAX) < ready_after_attempt {
            return Err(HandlerError::retry_after(
                7,
                "CustomRetry",
                format!("not ready on attempt {}", context.attempt()),
            ));
        }
        Ok(StepDecision::graceful_complete(String::from("ready")))
    }
}
