// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deletes StepExecution rows that ended Steps no longer need, so Step rows grow with active Steps.

use dexzd_keyspace::row_keys::{prefix_range, run_named_key};
use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    EndedStepRowSweep, RunRecord, StepExecutionPhase, StepExecutionRecord,
};
use prost::Message;

use crate::state_access::{FlowTransaction, RunRows};
use crate::{ApplyError, StateWrite};

/// Most Step rows one command visits while it deletes the ended rows older binaries kept.
const SWEEP_ROWS_PER_COMMAND: usize = 256;

/// Whether WaitForStepCompletion answers Completed for this ended Step with or without its row.
pub(crate) fn is_deletable_ended_step(step: &StepExecutionRecord) -> bool {
    // The handler's row stops a second Flow timeout handler from starting.
    if step.is_timeout_handler {
        return false;
    }
    match StepExecutionPhase::try_from(step.phase) {
        Ok(StepExecutionPhase::Completed) => true,
        // Failed Steps, close cancellations, and unmarked older cancellations answer from their rows.
        Ok(StepExecutionPhase::Canceled) => step.is_canceled_by_flow_close == Some(false),
        _ => false,
    }
}

/// Deletes the deletable Step rows this command wrote, after its event links and pending methods.
///
/// Returns an error when a written row does not decode.
pub(crate) fn delete_ended_step_rows(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
) -> Result<(), ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::StepExecution);
    let mut deletable_keys = Vec::new();
    for (key, value) in transaction.buffered_puts_with_prefix(ColumnFamily::Flow, &prefix) {
        if is_deletable_ended_step(&decode_step(key, value)?) {
            deletable_keys.push(key.to_vec());
        }
    }
    // Rows are put many times, so SingleDelete would be unsafe.
    for key in deletable_keys {
        transaction.delete(ColumnFamily::Flow, key);
    }
    Ok(())
}

/// Deletes the deletable ended rows that a run kept from older binaries, visiting at most
/// [`SWEEP_ROWS_PER_COMMAND`] Step rows after where the previous command stopped.
///
/// Live commands only: a replay cannot know the sweep progress of the command it repeats.
/// Returns an error when a visited row does not decode.
pub(crate) fn sweep_ended_step_rows(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    record: &mut RunRecord,
) -> Result<(), ApplyError> {
    let sweep = match &record.ended_step_row_sweep {
        Some(sweep) if sweep.is_complete => return Ok(()),
        Some(sweep) => sweep.clone(),
        None => EndedStepRowSweep::default(),
    };
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::StepExecution);
    let (mut start, end) = prefix_range(&prefix);
    if !sweep.last_visited_step_execution_id.is_empty() {
        start = run_named_key(
            rows.flow_hash,
            rows.run_id,
            RunTable::StepExecution,
            &sweep.last_visited_step_execution_id,
        );
        start.push(0);
    }
    let page = transaction.scan(ColumnFamily::Flow, &start, &end, SWEEP_ROWS_PER_COMMAND)?;
    let is_complete = page.len() < SWEEP_ROWS_PER_COMMAND;
    let mut last_visited_step_execution_id = String::new();
    for (key, value) in page {
        let step = decode_step(&key, &value)?;
        last_visited_step_execution_id =
            String::from_utf8(key[prefix.len()..].to_vec()).map_err(|error| {
                ApplyError::CorruptState {
                    table: "step execution",
                    key: key.iter().map(|byte| format!("{byte:02x}")).collect(),
                    detail: error.to_string(),
                }
            })?;
        if is_deletable_ended_step(&step) {
            transaction.delete(ColumnFamily::Flow, key);
        }
    }
    record.ended_step_row_sweep = Some(EndedStepRowSweep {
        last_visited_step_execution_id: if is_complete {
            String::new()
        } else {
            last_visited_step_execution_id
        },
        is_complete,
    });
    Ok(())
}

fn decode_step(key: &[u8], value: &[u8]) -> Result<StepExecutionRecord, ApplyError> {
    StepExecutionRecord::decode(value).map_err(|error| ApplyError::CorruptState {
        table: "step execution",
        key: key.iter().map(|byte| format!("{byte:02x}")).collect(),
        detail: error.to_string(),
    })
}

/// Returns whether `writes` put a row [`delete_ended_step_rows`] deletes: a command bypassed it.
pub(crate) fn has_deletable_ended_step_put(writes: &[StateWrite]) -> bool {
    writes.iter().any(|write| {
        let StateWrite::Put {
            column_family: ColumnFamily::Flow,
            key,
            value,
        } = write
        else {
            return false;
        };
        parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::StepExecution)
            && StepExecutionRecord::decode(value.as_slice())
                .is_ok_and(|step| is_deletable_ended_step(&step))
    })
}
