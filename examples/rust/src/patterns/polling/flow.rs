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
pub struct SimplePollingFlow {
    poll: SimplePoll,
}

impl Flow for SimplePollingFlow {
    type StartInput = u32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.poll)
    }
}

#[derive(Default)]
struct SimplePoll;

impl Step for SimplePoll {
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
            Ok(StepDecision::go_to(&SimplePoll, remaining - 1))
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
            return Err(HandlerError::new("external system is not ready"));
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
