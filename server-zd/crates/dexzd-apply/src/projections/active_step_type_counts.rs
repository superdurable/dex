// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Active Step executions counted by Step type, for the search document's active Step types.

use dexzd_keyspace::row_keys::run_singleton_key;
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dex::ActiveStepSearchMode;
use dexzd_protocol::dexzd::v1::{
    ActiveStepTypeCounts, RunRecord, StepExecutionPhase, StepExecutionRecord, flow_command::Command,
};
use prost::Message;

use crate::ApplyError;
use crate::command_handlers::CommandScope;
use crate::command_handlers::run_cursor::RunCursor;
use crate::state_access::FlowTransaction;

/// A Step execution that the counts include.
#[derive(Debug, PartialEq, Eq)]
struct CountedStep {
    step_type: String,
    has_wait_for: bool,
}

/// Updates the run record's active Step type counts from this command's Step execution writes.
///
/// Step execution writes after this call would escape the counts.
///
/// Returns an invariant violation when a change would make a count negative.
pub(crate) fn update_active_step_type_counts(
    transaction: &FlowTransaction<'_>,
    cursor: &mut RunCursor,
    scope: &CommandScope<'_>,
) -> Result<(), ApplyError> {
    // DISABLED keeps no counts and reads no Step execution.
    if active_step_search_mode(&cursor.record) == ActiveStepSearchMode::Disabled {
        cursor.record.search_active_step_type_counts = None;
        return Ok(());
    }
    // TimeTravel replaces every Step execution, so counting them again adds little.
    let is_time_travel = matches!(scope.command.command.as_ref(), Some(Command::TimeTravel(_)));
    let committed_counts = if is_time_travel {
        None
    } else {
        committed_counts(transaction, cursor)?
    };
    // A run without committed counts, such as an older run, counts every Step execution once.
    let counts = match committed_counts {
        Some(counts) => counts_after_step_changes(transaction, cursor, counts)?,
        None => count_step_executions(transaction, cursor)?,
    };
    cursor.record.search_active_step_type_counts = Some(counts);
    Ok(())
}

/// Returns the active Step types the run's search document lists: the counted Step types its
/// active Step search mode selects, in Step type order.
pub(crate) fn search_active_step_types(record: &RunRecord) -> Vec<String> {
    let mode = active_step_search_mode(record);
    let Some(counts) = record.search_active_step_type_counts.as_ref() else {
        return Vec::new();
    };
    counts
        .step_types
        .iter()
        .filter(|(_, count)| match mode {
            ActiveStepSearchMode::Disabled => false,
            ActiveStepSearchMode::EnabledForAll => {
                count.with_wait_for > 0 || count.without_wait_for > 0
            }
            ActiveStepSearchMode::Unspecified
            | ActiveStepSearchMode::EnabledForStepsWithWaitFor => count.with_wait_for > 0,
        })
        .map(|(step_type, _)| step_type.clone())
        .collect()
}

/// Returns the run's active Step search mode; an unknown value counts as UNSPECIFIED.
fn active_step_search_mode(record: &RunRecord) -> ActiveStepSearchMode {
    record
        .flow_config
        .as_ref()
        .and_then(|config| config.active_step_search_mode)
        .and_then(|mode| ActiveStepSearchMode::try_from(mode).ok())
        .unwrap_or(ActiveStepSearchMode::Unspecified)
}

/// Returns the counts of the run record before this command, which the committed Step
/// execution rows match.
fn committed_counts(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<Option<ActiveStepTypeCounts>, ApplyError> {
    let key = run_singleton_key(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::RunRecord,
    );
    Ok(transaction
        .get_committed_message::<RunRecord>(ColumnFamily::Flow, &key, "run record")?
        .and_then(|record| record.search_active_step_type_counts))
}

fn counts_after_step_changes(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    mut counts: ActiveStepTypeCounts,
) -> Result<ActiveStepTypeCounts, ApplyError> {
    let prefix = run_table_prefix(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::StepExecution,
    );
    for change in transaction.buffered_row_changes_with_prefix(ColumnFamily::Flow, &prefix)? {
        let previous = counted_step_of_row(change.previous.as_deref(), &change.key)?;
        let current = counted_step_of_row(change.current.as_deref(), &change.key)?;
        if previous == current {
            continue;
        }
        if let Some(step) = previous {
            remove_counted_step(&mut counts, &step)?;
        }
        if let Some(step) = current {
            add_counted_step(&mut counts, step);
        }
    }
    Ok(counts)
}

/// Counts every Step execution of the current attempt; the cost grows with the Step count.
fn count_step_executions(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<ActiveStepTypeCounts, ApplyError> {
    let mut counts = ActiveStepTypeCounts::default();
    for (_, step) in cursor.rows.step_executions(transaction)? {
        if let Some(step) = counted_step(step) {
            add_counted_step(&mut counts, step);
        }
    }
    Ok(counts)
}

fn counted_step_of_row(
    value: Option<&[u8]>,
    key: &[u8],
) -> Result<Option<CountedStep>, ApplyError> {
    let Some(bytes) = value else {
        return Ok(None);
    };
    let step = StepExecutionRecord::decode(bytes).map_err(|error| ApplyError::CorruptState {
        table: "step execution",
        key: key.iter().map(|byte| format!("{byte:02x}")).collect(),
        detail: error.to_string(),
    })?;
    Ok(counted_step(step))
}

/// Returns the counted form of an active Step execution other than the Flow timeout handler.
fn counted_step(step: StepExecutionRecord) -> Option<CountedStep> {
    if step.is_timeout_handler {
        return None;
    }
    let is_active = matches!(
        StepExecutionPhase::try_from(step.phase),
        Ok(StepExecutionPhase::WaitFor | StepExecutionPhase::Waiting | StepExecutionPhase::Execute)
    );
    if !is_active {
        return None;
    }
    let has_wait_for = !step
        .options
        .as_ref()
        .is_some_and(|options| options.skip_wait_for);
    Some(CountedStep {
        step_type: step.step_type,
        has_wait_for,
    })
}

fn add_counted_step(counts: &mut ActiveStepTypeCounts, step: CountedStep) {
    let count = counts.step_types.entry(step.step_type).or_default();
    if step.has_wait_for {
        count.with_wait_for += 1;
    } else {
        count.without_wait_for += 1;
    }
}

fn remove_counted_step(
    counts: &mut ActiveStepTypeCounts,
    step: &CountedStep,
) -> Result<(), ApplyError> {
    let negative_count = || ApplyError::InvariantViolation {
        detail: format!(
            "the active Step count of Step type {} became negative",
            step.step_type
        ),
    };
    let count = counts
        .step_types
        .get_mut(&step.step_type)
        .ok_or_else(negative_count)?;
    let bucket = if step.has_wait_for {
        &mut count.with_wait_for
    } else {
        &mut count.without_wait_for
    };
    *bucket = bucket.checked_sub(1).ok_or_else(negative_count)?;
    if count.with_wait_for == 0 && count.without_wait_for == 0 {
        counts.step_types.remove(&step.step_type);
    }
    Ok(())
}
