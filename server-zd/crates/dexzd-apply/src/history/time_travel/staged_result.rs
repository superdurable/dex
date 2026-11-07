// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What apply checks before it forks from a staged result (plan section 6.3 step 4).
//!
//! Every rejection here is ABORTED: the owner computes the result again and retries.

use dexzd_keyspace::row_keys::history_row_metadata_key;
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId, ValueReference};
use dexzd_protocol::dex::{ErrorSubStatus, FlowResetType, ResetFlowRequest};
use dexzd_protocol::dexzd::v1::{
    CommandReply, FlowCommand, HistoryRowMetadata, TimeTravelCommand, TimeTravelStagedTarget,
};

use super::TimeTravelResult;
use super::result_computation::{
    collect_external_inputs, pruned_input_rejection, selected_lineage,
};
use super::staging::{complete_staging, read_staged_chunks};
use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::run_cursor::RunCursor;
use crate::history::attempt_scope::AttemptRow;
use crate::history::lineage::{Lineage, LocalHistorySource, genesis_seq};
use crate::history::reset_target::TravelTarget;
use crate::history::state_rebuild::{HistoryPoint, newly_held_values};
use crate::state_access::FlowTransaction;
use crate::values::{is_stored_value, restore_unheld_value};

/// Reads the result that `command` names from the run's staging and checks it against the run.
///
/// The result gains the external input recorded after the command's computed_through_seq.
///
/// Returns the reply that rejects the command, or an error when stored state is corrupt.
pub(super) fn read_staged_result(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    command: &TimeTravelCommand,
    request: &ResetFlowRequest,
) -> Result<Result<TimeTravelResult, CommandReply>, ApplyError> {
    let staging = match complete_staging(cursor, &command.staging_id) {
        Ok(staging) => staging,
        Err(reply) => return Ok(Err(reply)),
    };
    let mut chunks = read_staged_chunks(transaction, cursor.rows, staging)?.into_iter();
    let mut first = chunks.next().unwrap_or_default();
    let target = first
        .target
        .take()
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "a staged time travel result has no target".to_owned(),
        })?;
    let (selected_attempt, lineage) =
        match selected_lineage(transaction, cursor.rows, &cursor.record, request)? {
            Ok(selected) => selected,
            Err(reply) => return Ok(Err(reply)),
        };
    if selected_attempt != target.selected_run_attempt {
        return Ok(Err(stale_result_reply(format!(
            "it was computed for run attempt {}, but the request selects run attempt \
             {selected_attempt}",
            target.selected_run_attempt
        ))));
    }
    let point = HistoryPoint {
        attempt: target.run_attempt,
        seq: target.run_seq,
    };
    if let Err(reply) = check_target_point(transaction, cursor, request, &lineage, point)? {
        return Ok(Err(reply));
    }
    let is_reapplying = !request.skip_writes_reapply;
    if let Some(reply) = pruned_input_rejection(&cursor.record, point.seq, is_reapplying) {
        return Ok(Err(reply));
    }
    let mut rows = Vec::new();
    let mut unstored_values = Vec::new();
    let mut reapplied_inputs = Vec::new();
    for chunk in std::iter::once(first).chain(chunks) {
        for row in &chunk.rows {
            rows.push(AttemptRow::from_checkpoint_row(row)?);
        }
        unstored_values.extend(chunk.values);
        reapplied_inputs.extend(chunk.reapplied_inputs);
    }
    if is_reapplying {
        match later_external_inputs(transaction, cursor, &lineage, command.computed_through_seq)? {
            Ok(inputs) => reapplied_inputs.extend(inputs),
            Err(reply) => return Ok(Err(reply)),
        }
    }
    Ok(Ok(TimeTravelResult {
        selected_attempt,
        target: travel_target(&target, point),
        run_status: target.run_status.unwrap_or_default(),
        rows,
        unstored_values,
        reapplied_inputs,
    }))
}

/// Writes the result's values the run no longer stores, then checks every value the restored
/// state holds anew.
///
/// Returns the reply naming the values the run still lacks; the caller discards the writes.
pub(super) fn restore_held_values(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    result: &TimeTravelResult,
) -> Result<Result<(), CommandReply>, ApplyError> {
    let rows = cursor.rows;
    for staged in &result.unstored_values {
        let (Some(value_id), Some(value)) = (staged.value_id, staged.value.clone()) else {
            return Err(ApplyError::InvariantViolation {
                detail: "a staged time travel value has no ID or no value".to_owned(),
            });
        };
        let value_id = ValueId {
            run_seq: value_id.run_seq,
            index_in_command: value_id.index_in_command,
        };
        if !is_stored_value(transaction, rows, value_id)? {
            restore_unheld_value(
                transaction,
                rows,
                value_id,
                staged.size_bytes,
                value,
                cursor.seq,
            )?;
        }
    }
    let mut missing = Vec::new();
    let newly_held = newly_held_values(
        transaction,
        rows,
        &cursor.record,
        &result.rows,
        &result.run_status,
    )?;
    for value_id in newly_held.into_keys() {
        if !is_stored_value(transaction, rows, value_id)? {
            missing.push(
                ValueReference {
                    run_id: rows.run_id,
                    value_id,
                }
                .to_string(),
            );
        }
    }
    if missing.is_empty() {
        return Ok(Ok(()));
    }
    Ok(Err(error_reply(
        grpc_code::ABORTED,
        ErrorSubStatus::Uncategorized,
        format!(
            "the staged time travel result lacks values that the restored state holds and the \
             run no longer stores: {}",
            missing.join(", ")
        ),
    )))
}

/// Checks the target is still selectable: a semantic lineage point in the window, or genesis for
/// BEGINNING.
fn check_target_point(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    request: &ResetFlowRequest,
    lineage: &Lineage,
    point: HistoryPoint,
) -> Result<Result<(), CommandReply>, ApplyError> {
    let rows = cursor.rows;
    let metadata: Option<HistoryRowMetadata> = transaction.get_message(
        ColumnFamily::Flow,
        &history_row_metadata_key(
            rows.flow_hash,
            rows.run_id,
            point.seq,
            RunTable::HistoryRecord,
            0,
        ),
        "history metadata",
    )?;
    let is_beginning = request.reset_type == FlowResetType::Beginning as i32;
    let is_genesis = point.seq == genesis_seq(transaction, rows)?;
    let is_selectable = metadata.is_some_and(|metadata| {
        metadata.attempt == point.attempt
            && lineage.contains(point.attempt, point.seq)
            && ((is_beginning && is_genesis)
                || (metadata.is_semantic && point.seq >= cursor.record.window_start_seq))
    });
    if is_selectable {
        return Ok(Ok(()));
    }
    Ok(Err(stale_result_reply(format!(
        "its target point (run attempt {}, run seq {}) is no longer a time travel target of the \
         window, which starts at run seq {}",
        point.attempt, point.seq, cursor.record.window_start_seq
    ))))
}

/// External input of the lineage recorded after `computed_through_seq`, which the staged
/// result does not cover.
///
/// Returns the rejection when a record of that range is no longer stored locally.
fn later_external_inputs(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    lineage: &Lineage,
    computed_through_seq: u64,
) -> Result<Result<Vec<FlowCommand>, CommandReply>, ApplyError> {
    let last_seq = cursor.seq.saturating_sub(1);
    if computed_through_seq > last_seq {
        return Ok(Err(stale_result_reply(format!(
            "it covers run seq {computed_through_seq}, after the run's last seq {last_seq}"
        ))));
    }
    let history = LocalHistorySource::new(transaction, cursor.rows);
    Ok(collect_external_inputs(
        &history,
        lineage,
        computed_through_seq + 1..=last_seq,
        cursor.record.window_start_seq,
    )?
    .map_err(|missing_seq| {
        stale_result_reply(format!(
            "the run no longer stores the history record at run seq {missing_seq} \
                     locally, whose external input would be reapplied"
        ))
    }))
}

fn travel_target(target: &TimeTravelStagedTarget, point: HistoryPoint) -> TravelTarget {
    TravelTarget {
        point,
        last_event_id: target.last_event_id,
        close_history_message: target.close_history_message.clone(),
    }
}

fn stale_result_reply(reason: String) -> CommandReply {
    error_reply(
        grpc_code::ABORTED,
        ErrorSubStatus::Uncategorized,
        format!("the staged time travel result is out of date: {reason}"),
    )
}
