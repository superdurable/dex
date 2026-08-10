// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList};

pub(crate) struct WorkflowUncompletedForceFailWorkflow {
    start: WorkflowUncompletedForceFailStep,
}

impl WorkflowUncompletedForceFailWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: WorkflowUncompletedForceFailStep,
        }
    }
}

impl Flow for WorkflowUncompletedForceFailWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedForceFailStep;

impl Step for WorkflowUncompletedForceFailStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, _input: i32) -> HandlerResult<StepDecision> {
        Ok(StepDecision::force_fail("a failing message"))
    }
}
