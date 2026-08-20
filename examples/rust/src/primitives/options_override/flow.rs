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
    StepMovement, StepOptions, Wait, WaitForFailurePolicy,
};

#[derive(Default)]
pub struct OptionsOverrideFlow {
    first: OverrideFirstStep,
    second: OverrideSecondStep,
}

impl Flow for OptionsOverrideFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

#[derive(Default)]
struct OverrideFirstStep;

impl Step for OverrideFirstStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        let override_options = StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
            .wait_for_failure(WaitForFailurePolicy::Proceed);
        let payload = format!("{input}_state1");
        Ok(StepDecision::go_to_many([StepMovement::to_with_options(
            &OverrideSecondStep,
            payload,
            override_options,
        )]))
    }
}

#[derive(Default)]
struct OverrideSecondStep;

impl Step for OverrideSecondStep {
    type Input = String;

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::FailFlow)
    }

    fn wait_for(&self, _context: &mut Context, _input: Self::Input) -> HandlerResult<Wait> {
        Err(HandlerError::new(
            "OptionsOverride",
            "state 2 wait failure",
        ))
    }

    fn execute(&self, context: &mut Context, input: Self::Input) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new(
                "OptionsOverride",
                "waitFor failure was not reported",
            ));
        }
        Ok(StepDecision::graceful_complete(format!("{input}_state2")))
    }
}
