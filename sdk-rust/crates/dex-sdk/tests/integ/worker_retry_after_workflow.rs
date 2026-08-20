// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

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
                WAIT_FOR_RETRY_AFTER_DETAIL,
            ));
        }
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, _context: &mut Context, _input: ()) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete("wait-retry-after".to_string()))
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
                EXECUTE_RETRY_AFTER_DETAIL,
            ));
        }
        Ok(StepDecision::graceful_complete("execute-retry-after".to_string()))
    }
}
