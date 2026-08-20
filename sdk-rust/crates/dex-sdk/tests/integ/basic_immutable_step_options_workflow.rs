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
    StepMovement, StepOptions, Wait, WaitForFailurePolicy,
};

pub(crate) struct BasicImmutableStepOptionsWorkflow {
    start: StartStep,
    failing_wait: FailingWaitStep,
}

impl BasicImmutableStepOptionsWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: StartStep,
            failing_wait: FailingWaitStep,
        }
    }
}

impl Flow for BasicImmutableStepOptionsWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.failing_wait)
    }
}

struct StartStep;

impl Step for StartStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        let options = StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::Proceed);
        Ok(StepDecision::go_to_many([StepMovement::to_with_options(
            &FailingWaitStep,
            1,
            options,
        )]))
    }
}

struct FailingWaitStep;

impl Step for FailingWaitStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, input: i32) -> HandlerResult<Wait> {
        Err(HandlerError::new(
            "BasicImmutableStepOptionsFailure",
            format!("expected wait failure {input}"),
        ))
    }

    fn execute(&self, context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new(
                "BasicImmutableStepOptionsFailure",
                "wait failure was not reported",
            ));
        }
        if input == 1 {
            return Ok(StepDecision::go_to(self, 2));
        }
        Ok(StepDecision::graceful_complete(input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::FailFlow)
    }
}
