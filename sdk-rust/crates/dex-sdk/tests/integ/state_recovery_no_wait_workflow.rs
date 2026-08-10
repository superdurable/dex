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
    StepOptions,
};

pub(crate) struct StateRecoveryNoWaitWorkflow {
    start: FailingNoWaitStep,
    recover: RecoverNoWaitStep,
}

impl StateRecoveryNoWaitWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: FailingNoWaitStep,
            recover: RecoverNoWaitStep,
        }
    }
}

impl Flow for StateRecoveryNoWaitWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.recover)
    }
}

struct FailingNoWaitStep;

impl Step for FailingNoWaitStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("execute failure"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .maximum_attempts(1)
                    .backoff_coefficient(2.0),
            )
            .on_execute_failure_proceed_to(&RecoverNoWaitStep)
    }
}

struct RecoverNoWaitStep;

impl Step for RecoverNoWaitStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if input == 10 {
            return Ok(StepDecision::graceful_complete(input));
        }
        if input == 5 {
            return Ok(StepDecision::go_to(&FailingNoWaitStep, input * 2));
        }
        Ok(StepDecision::force_fail(format!(
            "unexpected input {input}"
        )))
    }
}
