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
    StepAttemptFailedCommand, StepAttemptFailureKind, StepExecutionRecord, StepMethod,
};
use prost::Message;

/// A failed WaitFor whose Step moved on to Execute.
const WAIT_FOR_PROCEED: &str = "wait-for-proceed";
/// A failed Execute that started its configured recovery Step.
const EXECUTE_RECOVERY: &str = "execute-recovery";
/// A failed Flow timeout handler that started its configured recovery Step.
const TIMEOUT_HANDLER_RECOVERY: &str = "timeout-handler-recovery";
/// A failure because the retry policy's total duration expired.
const RETRY_DEADLINE: &str = "retry-deadline";

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
