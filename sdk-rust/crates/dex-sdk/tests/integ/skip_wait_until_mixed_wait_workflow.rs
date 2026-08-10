// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerResult, Step, StepDecision, StepList, StepOptions, Timer, Wait,
};

pub(crate) struct SkipWaitUntilMixedWaitWorkflow {
    first: FirstStep,
    second: SecondStep,
}

impl SkipWaitUntilMixedWaitWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: FirstStep,
            second: SecondStep,
        }
    }
}

impl Flow for SkipWaitUntilMixedWaitWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct FirstStep;

impl Step for FirstStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(&SecondStep, input + 1))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_method_timeout(Duration::from_secs(5))
    }
}

struct SecondStep;

impl Step for SecondStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(1))))
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input + 1))
    }
}
