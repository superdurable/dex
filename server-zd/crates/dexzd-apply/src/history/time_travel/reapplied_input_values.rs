// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Values that reapplied external input references, which the run must store before a time
//! travel reapplies it (plan section 6.3 step 4.4).

use std::collections::BTreeSet;

use dexzd_keyspace::row_keys::value_row_key;
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId, ValueReference};
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{
    CommandReply, StoredValueId, TimeTravelStagedInput, TimeTravelStagedValue, ValueBirthRecord,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::history::state_rebuild::{HistoryPoint, ReplayResources, replay_memory_exceeded_reply};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::{RpcOutputValue, command_values, is_stored_value, own_referenced_value_id};

/// Returns the IDs of the run's values that `inputs` name by value reference.
pub(super) fn reapplied_input_value_ids<'input>(
    inputs: impl IntoIterator<Item = &'input TimeTravelStagedInput>,
    rows: RunRows,
) -> BTreeSet<ValueId> {
    inputs
        .into_iter()
        .filter_map(|input| input.command.as_ref())
        .flat_map(|command| command_values(command, RpcOutputValue::Excluded))
        .filter_map(|value| own_referenced_value_id(value, rows))
        .collect()
}

/// Returns the values the reapplied `inputs` reference that the transaction's store lacks and
/// `staged` does not hold, read from offloaded values. Each value reserves its recorded size.
///
/// Returns a rejection naming the first value that no source has, or when the values exceed
/// the memory budget.
pub(super) fn unstored_input_values(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    inputs: &[TimeTravelStagedInput],
    staged: &[TimeTravelStagedValue],
    resources: ReplayResources<'_>,
    point: HistoryPoint,
) -> Result<Result<Vec<TimeTravelStagedValue>, CommandReply>, ApplyError> {
    let staged_ids: BTreeSet<ValueId> = staged
        .iter()
        .filter_map(|staged| staged.value_id)
        .map(|value_id| ValueId {
            run_seq: value_id.run_seq,
            index_in_command: value_id.index_in_command,
        })
        .collect();
    let mut values = Vec::new();
    let mut first_unavailable = None;
    for value_id in reapplied_input_value_ids(inputs, rows) {
        if staged_ids.contains(&value_id) || is_stored_value(transaction, rows, value_id)? {
            continue;
        }
        let birth: Option<ValueBirthRecord> = transaction.get_message(
            ColumnFamily::Flow,
            &value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id),
            "value birth",
        )?;
        // Apply restores a staged value only over its birth record.
        let Some(birth) = birth else {
            return Ok(Err(unavailable_input_value_reply(rows, value_id)));
        };
        if !resources.memory_budget.reserve(birth.size_bytes) {
            return Ok(Err(replay_memory_exceeded_reply(
                point,
                resources.memory_budget.limit_bytes(),
                "the values the reapplied external input references",
            )));
        }
        // Every value is asked for, so an owner learns all it must fetch at once.
        let Some(value) = resources
            .offloaded_values
            .offloaded_value(rows.run_id, value_id)?
        else {
            first_unavailable.get_or_insert(value_id);
            continue;
        };
        values.push(TimeTravelStagedValue {
            value_id: Some(StoredValueId {
                run_seq: value_id.run_seq,
                index_in_command: value_id.index_in_command,
            }),
            value: Some(value),
        });
    }
    if let Some(value_id) = first_unavailable {
        return Ok(Err(unavailable_input_value_reply(rows, value_id)));
    }
    Ok(Ok(values))
}

fn unavailable_input_value_reply(rows: RunRows, value_id: ValueId) -> CommandReply {
    let reference = ValueReference {
        run_id: rows.run_id,
        value_id,
    };
    error_reply(
        grpc_code::FAILED_PRECONDITION,
        ErrorSubStatus::Uncategorized,
        format!(
            "external input after the time travel target references value {reference}, which \
             the run no longer stores; set skip_writes_reapply to travel without reapplying \
             writes, or choose a later target point"
        ),
    )
}
