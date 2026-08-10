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
    Context, Flow, HandlerResult, RetryPolicy, Step, StepDecision, StepList, StepOptions,
};

pub(crate) struct WorkflowUncompletedEmptyDecisionWorkflow {
    start: WorkflowUncompletedEmptyDecisionStep,
}

impl WorkflowUncompletedEmptyDecisionWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: WorkflowUncompletedEmptyDecisionStep,
        }
    }
}

impl Flow for WorkflowUncompletedEmptyDecisionWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedEmptyDecisionStep;

impl Step for WorkflowUncompletedEmptyDecisionStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::go_to_many(std::iter::empty()))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_retry(RetryPolicy::new().maximum_attempts(1))
    }
}
