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

pub(crate) const WAIT_FOR_RETRY_AFTER_DETAIL: &str = "rust waitFor retry-after failure";
pub(crate) const EXECUTE_RETRY_AFTER_DETAIL: &str = "rust execute retry-after failure";
pub(crate) const RETRY_AFTER_SECONDS: i32 = 2;
pub(crate) const RETRY_POLICY_INTERVAL_SECONDS: u64 = 10;

pub(crate) struct WorkerRetryAfterWaitForWorkflow {
    start: WorkerRetryAfterWaitForStep,
}

impl WorkerRetryAfterWaitForWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: WorkerRetryAfterWaitForStep,
        }
    }
}

impl Flow for WorkerRetryAfterWaitForWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkerRetryAfterWaitForStep;

impl Step for WorkerRetryAfterWaitForStep {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(RETRY_POLICY_INTERVAL_SECONDS))
                    .maximum_attempts(3),
            )
            .wait_for_durability(StepDurability::Sync)
            .execute_durability(StepDurability::Sync)
    }

    fn wait_for(&self, context: &mut Context, _input: ()) -> HandlerResult<Wait> {
        if context.attempt() == 1 {
            return Err(HandlerError::retry_after(
                RETRY_AFTER_SECONDS,
                "WorkerRetryAfter",
                WAIT_FOR_RETRY_AFTER_DETAIL,
            ));
        }
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(
            "wait-retry-after".to_string(),
        ))
    }
}

pub(crate) struct WorkerRetryAfterExecuteWorkflow {
    start: WorkerRetryAfterExecuteStep,
}

impl WorkerRetryAfterExecuteWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: WorkerRetryAfterExecuteStep,
        }
    }
}

impl Flow for WorkerRetryAfterExecuteWorkflow {
    type StartInput = ();

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct WorkerRetryAfterExecuteStep;

impl Step for WorkerRetryAfterExecuteStep {
    type Input = ();

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(
                RetryPolicy::new()
                    .initial_interval(Duration::from_secs(RETRY_POLICY_INTERVAL_SECONDS))
                    .maximum_attempts(3),
            )
            .execute_durability(StepDurability::Sync)
    }

    fn execute(&self, context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        if context.attempt() == 1 {
            return Err(HandlerError::retry_after(
                RETRY_AFTER_SECONDS,
                "WorkerRetryAfter",
                EXECUTE_RETRY_AFTER_DETAIL,
            ));
        }
        Ok(StepDecision::graceful_complete(
            "execute-retry-after".to_string(),
        ))
    }
}
