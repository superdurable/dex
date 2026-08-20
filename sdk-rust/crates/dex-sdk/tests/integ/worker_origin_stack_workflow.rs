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
    Context, Flow, HandlerError, HandlerResult, RetryPolicy, Step, StepDecision, StepDurability,
    StepList, StepOptions, Wait,
};

pub(crate) const ORIGIN_STACK_DETAIL: &str = "rust origin stack failure";

pub(crate) struct WorkerOriginStackWaitForWorkflow {
    start: WorkerOriginStackWaitForStep,
}

impl WorkerOriginStackWaitForWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: WorkerOriginStackWaitForStep,
        }
    }
}

impl Flow for WorkerOriginStackWaitForWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkerOriginStackWaitForStep;

impl Step for WorkerOriginStackWaitForStep {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(2))
                    .maximum_attempts(3),
            )
            .wait_for_durability(StepDurability::Sync)
            .execute_durability(StepDurability::Sync)
    }

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        if context.attempt() == 1 {
            return Err(origin_stack_failure());
        }
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("origin-stack".to_string()))
    }
}

fn origin_stack_failure() -> HandlerError {
    HandlerError::new(ORIGIN_STACK_DETAIL)
}
