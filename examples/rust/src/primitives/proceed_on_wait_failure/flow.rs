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
    StepOptions, Wait, WaitForFailurePolicy,
};

#[derive(Default)]
pub struct ProceedOnWaitFailureFlow {
    failing_wait: FailingWaitStep,
    finish: FinishStep,
}

impl Flow for ProceedOnWaitFailureFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.failing_wait).and(&self.finish)
    }
}

#[derive(Default)]
struct FailingWaitStep;

impl Step for FailingWaitStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_failure(WaitForFailurePolicy::Proceed)
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
    }

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Err(HandlerError::new(
            "ProceedOnWaitFailure",
            "planned WaitFor failure",
        ))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new(
                "ProceedOnWaitFailure",
                "waitFor failure was not reported",
            ));
        }
        Ok(StepDecision::go_to(
            &FinishStep,
            format!("{input}_recovered"),
        ))
    }
}

#[derive(Default)]
struct FinishStep;

impl Step for FinishStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}
