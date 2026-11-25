// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Sustainable Use License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::collections::HashMap;
use std::sync::{LazyLock, Mutex};
use std::time::Duration;

use dex_sdk::{
    Context, Flow, HandlerError, HandlerResult, RetryPolicy, Step, StepDecision, StepList,
    StepOptions, Wait, WaitForFailurePolicy,
};

pub(crate) const NON_RETRYABLE_ERROR_TYPE: &str = "CardRejected";
pub(crate) const NON_RETRYABLE_DETAIL: &str = "rust card rejected";

/// Handler calls per Flow ID; the Worker runs in the test process.
pub(crate) static ATTEMPTS_BY_FLOW_ID: LazyLock<Mutex<HashMap<String, u32>>> =
    LazyLock::new(|| Mutex::new(HashMap::new()));

/// A real business retry policy: unlimited attempts an hour apart.
fn hourly_retry() -> RetryPolicy {
    RetryPolicy::new()
        .initial_interval(Duration::from_secs(3600))
        .maximum_interval(Duration::from_secs(3600))
}

fn fail_permanently(context: &Context) -> HandlerError {
    *ATTEMPTS_BY_FLOW_ID
        .lock()
        .expect("attempt counter lock")
        .entry(context.flow_id().to_string())
        .or_default() += 1;
    HandlerError::non_retryable(NON_RETRYABLE_ERROR_TYPE, NON_RETRYABLE_DETAIL)
}

fn complete_with_recovery(context: &Context) -> HandlerResult<StepDecision> {
    let Some(recovery) = context.recovery_error() else {
        return Err(HandlerError::non_retryable(
            "MissingRecovery",
            "recovery error is missing",
        ));
    };
    Ok(StepDecision::graceful_complete(format!(
        "{}: {}",
        recovery.error_type(),
        recovery.detail()
    )))
}

pub(crate) struct NonRetryableExecuteWorkflow {
    start: NonRetryableChargeStep,
    recover: NonRetryableRecoveryStep,
}

impl NonRetryableExecuteWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: NonRetryableChargeStep,
            recover: NonRetryableRecoveryStep,
        }
    }
}

impl Flow for NonRetryableExecuteWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start).and(&self.recover)
    }
}

struct NonRetryableChargeStep;

impl Step for NonRetryableChargeStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        Err(fail_permanently(context))
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_retry(hourly_retry())
            .on_execute_failure_proceed_to(&NonRetryableRecoveryStep)
    }
}

struct NonRetryableRecoveryStep;

impl Step for NonRetryableRecoveryStep {
    type Input = String;

    fn wait_for(&self, _context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Ok(Wait::skip_immediately())
    }

    fn execute(&self, context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        complete_with_recovery(context)
    }
}

pub(crate) struct NonRetryableWaitForWorkflow {
    start: NonRetryableWaitForStep,
}

impl NonRetryableWaitForWorkflow {
    pub(crate) fn new() -> Self {
        Self {
            start: NonRetryableWaitForStep,
        }
    }
}

impl Flow for NonRetryableWaitForWorkflow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start)
    }
}

struct NonRetryableWaitForStep;

impl Step for NonRetryableWaitForStep {
    type Input = String;

    fn wait_for(&self, context: &mut Context, _input: String) -> HandlerResult<Wait> {
        Err(fail_permanently(context))
    }

    fn execute(&self, context: &mut Context, _input: String) -> HandlerResult<StepDecision> {
        complete_with_recovery(context)
    }

    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .wait_for_retry(hourly_retry())
            .wait_for_failure(WaitForFailurePolicy::Proceed)
    }
}
