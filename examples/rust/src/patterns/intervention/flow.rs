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

use dex_sdk::{
    Channel, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, RetryPolicy, Step,
    StepDecision, StepList, StepOptions, Wait,
};

use std::time::Duration;

#[derive(Default)]
pub struct ManualRecoveryFlow {
    do_work_step: DoWorkStep,
    manual_step: ManualStep,
}

impl Flow for ManualRecoveryFlow {
    type StartInput = bool;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.do_work_step).and(&self.manual_step)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().channel(&retry()).channel(&skip())
    }
}

#[derive(Default)]
struct DoWorkStep;

impl Step for DoWorkStep {
    type Input = bool;

    fn execute(
        &self,
        _context: &mut Context,
        should_fail: Self::Input,
    ) -> HandlerResult<StepDecision> {
        if should_fail {
            return Err(HandlerError::new("DoWorkStep", "work failed"));
        }
        Ok(StepDecision::graceful_complete(String::from(
            "work completed",
        )))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(1))
                    .backoff_coefficient(2.0)
                    .maximum_interval(Duration::from_secs(4))
                    .maximum_attempts(4),
            )
            .on_execute_failure_proceed_to(&ManualStep)
    }
}

#[derive(Default)]
struct ManualStep;

impl Step for ManualStep {
    type Input = bool;

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([retry().for_one(), skip().for_one()]))
    }

    fn execute(&self, context: &mut Context, _input: Self::Input) -> HandlerResult<StepDecision> {
        if !retry().condition_results(context)?.is_empty() {
            return Ok(StepDecision::go_to(&DoWorkStep, false));
        }
        Ok(StepDecision::force_fail("manual recovery skipped"))
    }
}

fn retry() -> Channel<()> {
    Channel::new("manual-recovery-retry")
}

fn skip() -> Channel<()> {
    Channel::new("manual-recovery-skip")
}
