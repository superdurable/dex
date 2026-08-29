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
    Context, Flow, HandlerError, HandlerResult, RetryPolicy, Step, StepDecision, StepList,
    StepOptions, Timer, Wait,
};

#[derive(Default)]
pub struct PollingWithTimerFlow {
    poll: PollingStep,
}

impl Flow for PollingWithTimerFlow {
    type StartInput = u32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.poll)
    }
}

#[derive(Default)]
struct PollingStep;

impl Step for PollingStep {
    type Input = u32;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(5))))
    }

    fn execute(
        &self,
        _context: &mut Context,
        remaining: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if remaining <= 1 {
            Ok(StepDecision::graceful_complete("ready".to_string()))
        } else {
            Ok(StepDecision::go_to(&PollingStep, remaining - 1))
        }
    }
}

#[derive(Default)]
pub struct BackoffPollingFlow {
    poll: BackoffPoll,
}

impl Flow for BackoffPollingFlow {
    type StartInput = u32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.poll)
    }
}

#[derive(Default)]
struct BackoffPoll;

impl Step for BackoffPoll {
    type Input = u32;

    fn execute(
        &self,
        context: &mut Context,
        ready_after_attempt: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if context.attempt() < ready_after_attempt.max(1) {
            return Err(HandlerError::new("Polling", "external system is not ready"));
        }
        Ok(StepDecision::graceful_complete("ready".to_string()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(
            RetryPolicy::new()
                .initial_interval(Duration::from_secs(1))
                .backoff_coefficient(2.0)
                .maximum_interval(Duration::from_secs(30))
                .maximum_attempts(8),
        )
    }
}

#[derive(Default)]
pub struct IterationFlow {
    iteration: IterationStep,
}

impl Flow for IterationFlow {
    type StartInput = String;
    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.iteration)
    }
}

#[derive(Default)]
struct IterationStep;

impl Step for IterationStep {
    type Input = String;
    fn step_type(&self) -> &'static str {
        "IterationStep"
    }
    fn execute(&self, _: &mut Context, page_token: String) -> HandlerResult<StepDecision> {
        let next_page_token = match page_token.as_str() {
            "" => "page-2",
            "page-2" => "page-3",
            _ => "",
        };
        if next_page_token.is_empty() {
            Ok(StepDecision::graceful_complete(()))
        } else {
            Ok(StepDecision::go_to(
                &IterationStep,
                next_page_token.to_owned(),
            ))
        }
    }
}
