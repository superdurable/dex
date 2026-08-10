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
    Context, Flow, HandlerResult, RetryPolicy, Step, StepDecision, StepDurability, StepList,
    StepOptions,
};

pub(crate) struct WorkflowUncompletedStateTimeoutWorkflow {
    start: WorkflowUncompletedStateTimeoutStep,
}

impl WorkflowUncompletedStateTimeoutWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: WorkflowUncompletedStateTimeoutStep,
        }
    }
}

impl Flow for WorkflowUncompletedStateTimeoutWorkflow {
    type StartInput = i32;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkflowUncompletedStateTimeoutStep;

impl Step for WorkflowUncompletedStateTimeoutStep {
    type Input = i32;

    fn execute(&self, _context: &mut Context, input: i32) -> HandlerResult<StepDecision> {
        std::thread::sleep(Duration::from_secs(2));
        Ok(StepDecision::graceful_complete(input))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_method_timeout(Duration::from_secs(1))
            .execute_retry(RetryPolicy::new().maximum_attempts(1))
            .execute_durability(StepDurability::Sync)
    }
}
