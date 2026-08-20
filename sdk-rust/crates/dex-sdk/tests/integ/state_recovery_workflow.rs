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
    StepOptions, Wait,
};

pub(crate) struct StateRecoveryWorkflow {
    start: FailingStep,
    recover: RecoverStep,
}

impl StateRecoveryWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: FailingStep,
            recover: RecoverStep,
        }
    }
}

impl Flow for StateRecoveryWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.recover)
    }
}

struct FailingStep;

impl Step for FailingStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Err(HandlerError::new("StateRecoveryFailure", "execute failure"))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .maximum_attempts(1)
                    .backoff_coefficient(2.0),
            )
            .on_execute_failure_proceed_to(&RecoverStep)
    }
}

struct RecoverStep;

impl Step for RecoverStep {
    type Input = i32;

    fn wait_for(&self, _context: &mut Context, _input: i32) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        if input == 10 {
            return Ok(StepDecision::graceful_complete(input));
        }
        if input == 5 {
            return Ok(StepDecision::go_to(&FailingStep, input * 2));
        }
        Ok(StepDecision::force_fail(format!(
            "unexpected input {input}"
        )))
    }
}
