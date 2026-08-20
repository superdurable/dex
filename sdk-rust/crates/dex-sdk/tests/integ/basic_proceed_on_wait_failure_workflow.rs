// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, RetryPolicy, Step, StepDecision, StepList,
    StepOptions, Wait, WaitForFailurePolicy,
};

pub(crate) struct BasicProceedOnWaitFailureWorkflow {
    first: FailingWaitStep,
    second: CompleteStep,
}

impl BasicProceedOnWaitFailureWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: FailingWaitStep,
            second: CompleteStep,
        }
    }
}

impl Flow for BasicProceedOnWaitFailureWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct FailingWaitStep;

impl Step for FailingWaitStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Err(HandlerError::new(
            "BasicProceedOnWaitFailureFailure",
            "wait failure",
        ))
    }

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to(
            &CompleteStep,
            format!("{input}-recovered"),
        ))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_failure(WaitForFailurePolicy::Proceed)
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
    }
}

struct CompleteStep;

impl Step for CompleteStep {
    type Input = String;

    fn execute(&self, _context: &mut Context, input: String) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(input))
    }
}
