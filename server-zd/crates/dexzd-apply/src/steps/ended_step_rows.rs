// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deletes StepExecution rows that ended Steps no longer need, so Step rows grow with active Steps.

use dexzd_keyspace::{ColumnFamily, RunTable, parse_run_key, run_table_prefix};
use dexzd_protocol::dexzd::v1::{StepExecutionPhase, StepExecutionRecord};
use prost::Message;

use crate::state_access::{FlowTransaction, RunRows};
use crate::{ApplyError, StateWrite};

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
        let step =
            StepExecutionRecord::decode(value).map_err(|error| ApplyError::CorruptState {
                table: "step execution",
                key: key.iter().map(|byte| format!("{byte:02x}")).collect(),
                detail: error.to_string(),
            })?;
        if is_deletable_ended_step(&step) {
            deletable_keys.push(key.to_vec());
        }
    }
    // Rows are put many times, so SingleDelete would be unsafe.
    for key in deletable_keys {
        transaction.delete(ColumnFamily::Flow, key);
    }
    Ok(())
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
