// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The run's limit on active Step executions, `RunSettings.max_active_step_executions`.

use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::flow_command::Command;
use dexzd_protocol::dexzd::v1::{RunRecord, StepExecutionRecord};
use prost::Message;

use crate::ApplyError;
use crate::state_access::{FlowTransaction, RunRows};
use crate::steps::is_active;

/// Returns whether the command can start Steps from a Client request or a Worker decision.
/// Engine commands, such as recovery Steps and the timeout handler, never raise the count.
pub(crate) fn should_enforce_active_step_limit(body: &Command) -> bool {
    matches!(
        body,
        Command::FlowStart(_) | Command::StepMethodCompleted(_) | Command::RpcCommitted(_)
    )
}

/// Checks the run's active Step executions after the command's buffered writes; `record` is
/// the run record that the command wrote.
///
/// Returns the failure detail when the command raised the count and left it above the run's
/// `max_active_step_executions`, or `None` otherwise. A run that does not record
/// `enforce_active_step_limit`, or records a zero maximum, has no limit.
pub(crate) fn active_step_limit_violation(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
) -> Result<Option<String>, ApplyError> {
    // Older binaries recorded a maximum without enforcing it, so their log replays unchanged.
    let max_active_step_executions = record
        .settings
        .as_ref()
        .filter(|settings| settings.enforce_active_step_limit)
        .map_or(0, |settings| settings.max_active_step_executions);
    if max_active_step_executions == 0 {
        return Ok(None);
    }
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::StepExecution);
    let mut added_count: i64 = 0;
    for change in transaction.buffered_row_changes_with_prefix(ColumnFamily::Flow, &prefix)? {
        let was_counted = is_counted_row(change.previous.as_deref(), &change.key)?;
        let is_counted_now = is_counted_row(change.current.as_deref(), &change.key)?;
        added_count += i64::from(is_counted_now) - i64::from(was_counted);
    }
    // A command that does not raise the count needs no scan, even on a run above the limit.
    if added_count <= 0 {
        return Ok(None);
    }
    let active_count = active_step_count(transaction, rows, record)?;
    if active_count <= u64::from(max_active_step_executions) {
        return Ok(None);
    }
    Ok(Some(format!(
        "{active_count} active Step executions exceed the maximum of \
         {max_active_step_executions} (limits.maxConcurrentStepsPerFlow)"
    )))
}

/// Returns the active Step executions of the run after the command.
///
/// The search counts, which the command already refreshed, count the same Step executions.
/// A run whose active Step search is DISABLED keeps none, so its Step rows are scanned.
fn active_step_count(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    record: &RunRecord,
) -> Result<u64, ApplyError> {
    if let Some(counts) = record.search_active_step_type_counts.as_ref() {
        return Ok(counts
            .step_types
            .values()
            .map(|count| count.with_wait_for + count.without_wait_for)
            .sum());
    }
    let scanned_count = rows
        .step_executions(transaction)?
        .iter()
        .filter(|(_, step)| is_counted_step(step))
        .count();
    Ok(scanned_count as u64)
}

fn is_counted_row(value: Option<&[u8]>, key: &[u8]) -> Result<bool, ApplyError> {
    let Some(bytes) = value else {
        return Ok(false);
    };
    let step = StepExecutionRecord::decode(bytes).map_err(|error| ApplyError::CorruptState {
        table: "step execution",
        key: key.iter().map(|byte| format!("{byte:02x}")).collect(),
        detail: error.to_string(),
    })?;
    Ok(is_counted_step(&step))
}

/// The Flow timeout handler is a system Step, so it never counts.
fn is_counted_step(step: &StepExecutionRecord) -> bool {
    !step.is_timeout_handler && is_active(step)
}
