// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Counts what Step failure policies did when committed StepAttemptFailed commands applied.

use std::collections::BTreeMap;

use dexzd_apply::StateWrite;
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key};
use dexzd_protocol::dexzd::v1::{
    StepAttemptFailedCommand, StepAttemptFailureKind, StepExecutionRecord, StepInvocationPhase,
    StepMethod,
};
use prost::Message;

/// A failed WaitFor whose Step moved on to Execute.
const WAIT_FOR_PROCEED: &str = "wait-for-proceed";
/// A failed Execute that started its configured recovery Step.
const EXECUTE_RECOVERY: &str = "execute-recovery";
/// A failed Flow timeout handler that started its configured recovery Step.
const TIMEOUT_HANDLER_RECOVERY: &str = "timeout-handler-recovery";
/// A `RETRY_DEADLINE` failure, which the engine no longer produces (plan D74).
pub(crate) const RETRY_DEADLINE: &str = "retry-deadline";
/// A retryable failure that ended its method call because the retry total duration expired.
const RETRY_TOTAL_DURATION_EXHAUSTED: &str = "retry-total-duration-exhausted";

/// Adds what the applied `failed` command's `writes` show to `counts`.
pub(crate) fn count_step_failure_recoveries(
    failed: &StepAttemptFailedCommand,
    writes: &[StateWrite],
    counts: &mut BTreeMap<&'static str, u64>,
) {
    if failed.kind == StepAttemptFailureKind::RetryDeadline as i32 {
        *counts.entry(RETRY_DEADLINE).or_default() += 1;
    }
    let Some(invocation) = failed.invocation.as_ref() else {
        return;
    };
    let failed_step_id = invocation.step_execution_id.as_str();
    let written_steps = written_step_executions(writes);
    let failed_step = written_steps
        .iter()
        .find(|step| step.step_execution_id == failed_step_id);
    if failed_step
        .is_some_and(|step| is_ended_by_retry_total_duration(step, failed, invocation.method))
    {
        *counts.entry(RETRY_TOTAL_DURATION_EXHAUSTED).or_default() += 1;
    }
    let has_wait_for_proceeded = invocation.method == StepMethod::WaitFor as i32
        && failed_step.is_some_and(|step| {
            step.condition_results
                .as_ref()
                .is_some_and(|results| results.wait_for_failed)
        });
    if has_wait_for_proceeded {
        *counts.entry(WAIT_FOR_PROCEED).or_default() += 1;
    }
    let has_started_recovery_step = written_steps.iter().any(|step| {
        step.recovery_error.is_some()
            && step.step_execution_id != failed_step_id
            && step.from_step_execution_id == failed_step_id
    });
    if has_started_recovery_step {
        let name = if failed_step.is_some_and(|step| step.is_timeout_handler) {
            TIMEOUT_HANDLER_RECOVERY
        } else {
            EXECUTE_RECOVERY
        };
        *counts.entry(name).or_default() += 1;
    }
}

/// Returns whether a retryable failure ended the method call before its maximum attempts, which
/// only an expired retry total duration does.
fn is_ended_by_retry_total_duration(
    step: &StepExecutionRecord,
    failed: &StepAttemptFailedCommand,
    method: i32,
) -> bool {
    let options = step.options.clone().unwrap_or_default();
    let policy = if method == StepMethod::WaitFor as i32 {
        options.wait_for_retry_policy
    } else {
        options.execute_retry_policy
    };
    let Some(policy) = policy.filter(|policy| policy.total_duration_seconds > 0) else {
        return false;
    };
    let maximum_attempts = u32::try_from(policy.maximum_attempts).unwrap_or(0);
    let has_attempts_left = maximum_attempts == 0 || failed.attempt < maximum_attempts;
    let is_retrying = step.invocation.as_ref().is_some_and(|invocation| {
        invocation.method == method && invocation.phase == StepInvocationPhase::Backoff as i32
    });
    !failed.is_non_retryable && has_attempts_left && !is_retrying
}

fn written_step_executions(writes: &[StateWrite]) -> Vec<StepExecutionRecord> {
    writes
        .iter()
        .filter_map(|write| match write {
            StateWrite::Put {
                column_family: ColumnFamily::Flow,
                key,
                value,
            } => {
                let parsed = parse_run_key(key).ok()?;
                (parsed.table == RunTable::StepExecution)
                    .then(|| StepExecutionRecord::decode(value.as_slice()))
            }
            _ => None,
        })
        .map(|decoded| decoded.expect("apply writes decodable Step execution rows"))
        .collect()
}
