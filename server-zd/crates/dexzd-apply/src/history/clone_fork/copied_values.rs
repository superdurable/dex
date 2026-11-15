// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The value-table values that copied attempt rows reference.

use std::collections::BTreeMap;

use dexzd_keyspace::{RunTable, ValueId};
use dexzd_protocol::dexzd::v1::{
    CheckpointRow, ConditionResultsRecord, StepExecutionRecord, StoredValue, stored_value,
};
use prost::Message;

use crate::ApplyError;
use crate::values::held_table_values;

/// Returns every value-table value that `rows` reference, with its size.
///
/// These are the values the rows hold, and the child outputs in Step condition results. The
/// source run holds those outputs through its run-scoped SubFlowResult rows, which a copy lacks.
///
/// Returns an error when a row is corrupt.
pub(super) fn referenced_table_values(
    rows: &[CheckpointRow],
) -> Result<BTreeMap<ValueId, u64>, ApplyError> {
    let mut values = held_table_values(rows)?;
    for row in rows {
        if row.table != u32::from(RunTable::StepExecution as u8) {
            continue;
        }
        let step = StepExecutionRecord::decode(row.value.as_slice()).map_err(|error| {
            ApplyError::InvariantViolation {
                detail: format!("a copied Step execution row is corrupt: {error}"),
            }
        })?;
        let snapshot_results = step
            .invocation
            .as_ref()
            .and_then(|invocation| invocation.snapshot.as_ref())
            .and_then(|snapshot| snapshot.condition_results.as_ref());
        for results in step.condition_results.iter().chain(snapshot_results) {
            insert_child_outputs(&mut values, results);
        }
    }
    Ok(values)
}

fn insert_child_outputs(values: &mut BTreeMap<ValueId, u64>, results: &ConditionResultsRecord) {
    let outputs = results
        .sub_flow_results
        .iter()
        .flat_map(|result| &result.results)
        .filter_map(|output| output.completed_step_output.as_ref());
    for stored in outputs {
        if let Some((value_id, size_bytes)) = table_value_id(stored) {
            values.insert(value_id, size_bytes);
        }
    }
}

fn table_value_id(stored: &StoredValue) -> Option<(ValueId, u64)> {
    let Some(stored_value::Kind::TableValue(reference)) = &stored.kind else {
        return None;
    };
    let value_id = reference.value_id?;
    Some((
        ValueId {
            run_seq: value_id.run_seq,
            index_in_command: value_id.index_in_command,
        },
        reference.size_bytes,
    ))
}
