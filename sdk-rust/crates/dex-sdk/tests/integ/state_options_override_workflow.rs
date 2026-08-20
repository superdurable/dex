// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::sync::Mutex;

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, RetryPolicy, Step, StepDecision, StepList,
    StepMovement, StepOptions, Wait, WaitForFailurePolicy,
};

pub(crate) struct StateOptionsOverrideWorkflow {
    first: OverrideFirstStep,
    second: CompleteStep,
}

impl StateOptionsOverrideWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            first: OverrideFirstStep {
                output: Mutex::new(String::new()),
            },
            second: CompleteStep {
                output: Mutex::new(String::new()),
            },
        }
    }
}

impl Flow for StateOptionsOverrideWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.first).and(&self.second)
    }
}

struct OverrideFirstStep {
    output: Mutex<String>,
}

impl Step for OverrideFirstStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, input: String) -> HandlerResult<Wait> {
        *self.output.lock().expect("first output lock") = format!("{input}_state1_start");
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        let options = StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(2))
            .wait_for_failure(WaitForFailurePolicy::Proceed);
        let mut output = self.output.lock().expect("first output lock");
        output.push_str("_state1_decide");
        Ok(StepDecision::go_to_many([StepMovement::to_with_options(
            &CompleteStep {
                output: Mutex::new(String::new()),
            },
            output.clone(),
            options,
        )]))
    }
}

struct CompleteStep {
    output: Mutex<String>,
}

impl Step for CompleteStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, input: String) -> HandlerResult<Wait> {
        *self.output.lock().expect("second output lock") = format!("{input}_state2_start");
        Err(HandlerError::new(
            "StateOptionsOverrideFailure",
            "state 2 wait failure",
        ))
    }

    fn execute(&self, context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        if !context.wait_for_method_failed() {
            return Err(HandlerError::new(
                "StateOptionsOverrideFailure",
                "waitFor failure was not reported",
            ));
        }
        let mut output = self.output.lock().expect("second output lock");
        output.push_str("_state2_decide");
        Ok(StepDecision::graceful_complete(output.clone()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(RetryPolicy::new().maximum_attempts(1))
            .wait_for_failure(WaitForFailurePolicy::FailFlow)
    }
}
