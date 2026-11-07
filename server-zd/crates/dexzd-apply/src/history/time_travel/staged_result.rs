// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! What apply checks before it forks from a staged result (plan section 6.3 step 4).
//!
//! Out-of-date results are rejected with ABORTED, and the owner computes again; mismatched ones
//! with INVALID_ARGUMENT.

use std::collections::BTreeSet;

use dexzd_keyspace::row_keys::{history_row_metadata_key, value_row_key};
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId, ValueReference};
use dexzd_protocol::dex::{ErrorSubStatus, FlowResetType, ResetFlowRequest};
use dexzd_protocol::dexzd::v1::{
    CommandReply, HistoryRowMetadata, TimeTravelCommand, TimeTravelStagedInput,
    TimeTravelStagedTarget, ValueBirthRecord,
};
use prost::Message;

use super::TimeTravelResult;
use super::reapplied_input_values::reapplied_input_value_ids;
use super::result_computation::{
    InputCollectionFailure, collect_external_inputs, pruned_input_rejection, selected_lineage,
};
use super::staging::{complete_staging, read_staged_chunks};
use crate::ApplyError;
use crate::command_handlers::CommandScope;
use crate::command_handlers::command_replies::{error_reply, grpc_code};
use crate::command_handlers::run_cursor::RunCursor;
use crate::history::attempt_scope::AttemptRow;
use crate::history::lineage::{Lineage, LocalHistorySource, genesis_seq};
use crate::history::reset_target::TravelTarget;
use crate::history::state_rebuild::{
    HeldValueScan, HistoryPoint, ReplayMemoryBudget, encoded_bytes, newly_held_values,
};
use crate::state_access::FlowTransaction;
use crate::values::{is_stored_value, restore_unheld_value};

/// Reads the result that `command` names from the run's staging and checks it against the
/// command and the run.
///
/// The result gains the external input recorded after the command's computed_through_seq.
///
/// Returns the reply that rejects the command, or an error when stored state is corrupt.
pub(super) fn read_staged_result(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    scope: &CommandScope<'_>,
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
    let expected_run_attempt = match scope.command.expected_run_attempt {
        0 => cursor.record.current_attempt,
        expected => expected,
    };
    if let Err(reply) = check_staging_binding(&target, command, request, expected_run_attempt) {
        return Ok(Err(reply));
    }
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
    if let Err(reply) = check_staged_inputs(&reapplied_inputs, point.seq, &target, is_reapplying) {
        return Ok(Err(reply));
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

/// Checks that the staging was computed for this TimeTravel: the same request, run attempt,
/// computed seq, and target point.
fn check_staging_binding(
    target: &TimeTravelStagedTarget,
    command: &TimeTravelCommand,
    request: &ResetFlowRequest,
    expected_run_attempt: u32,
) -> Result<(), CommandReply> {
    let mismatch = if target.request.as_ref() != Some(request) {
        "a different request".to_owned()
    } else if target.expected_run_attempt != expected_run_attempt {
        format!(
            "run attempt {}, but the command expects run attempt {expected_run_attempt}",
            target.expected_run_attempt
        )
    } else if target.computed_through_seq != command.computed_through_seq {
        format!(
            "run seq {}, but the command names run seq {}",
            target.computed_through_seq, command.computed_through_seq
        )
    } else if (target.run_attempt, target.run_seq)
        != (command.target_run_attempt, command.target_run_seq)
    {
        format!(
            "the target point (run attempt {}, run seq {}), but the command names (run attempt \
             {}, run seq {})",
            target.run_attempt, target.run_seq, command.target_run_attempt, command.target_run_seq
        )
    } else {
        return Ok(());
    };
    Err(error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        format!(
            "the staged time travel result does not match its TimeTravel command: it was \
             computed for {mismatch}"
        ),
    ))
}

/// Checks staged input: none without reapply, otherwise after the target through the computed
/// seq in increasing (run seq, index).
fn check_staged_inputs(
    inputs: &[TimeTravelStagedInput],
    target_seq: u64,
    target: &TimeTravelStagedTarget,
    is_reapplying: bool,
) -> Result<(), CommandReply> {
    let invalid = |detail: String| {
        Err(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!("the staged time travel result has invalid input: {detail}"),
        ))
    };
    if !is_reapplying && !inputs.is_empty() {
        return invalid("the request skips reapplying writes".to_owned());
    }
    let mut previous_position = None;
    for input in inputs {
        if input.run_seq <= target_seq || input.run_seq > target.computed_through_seq {
            return invalid(format!(
                "run seq {} is outside run seqs {} to {}",
                input.run_seq,
                target_seq + 1,
                target.computed_through_seq
            ));
        }
        let position = (input.run_seq, input.index_in_record);
        if previous_position.is_some_and(|previous| previous >= position) {
            return invalid(format!(
                "run seq {} index {} is out of order or repeated",
                input.run_seq, input.index_in_record
            ));
        }
        previous_position = Some(position);
    }
    Ok(())
}

/// Writes the result's values the run no longer stores, then checks every value the restored
/// state holds anew and every value its reapplied input references.
///
/// A staged value is restored only over its birth record, whose recorded size it must match.
///
/// Returns the ABORTED reply naming a value that is staged twice, has no birth record, has
/// another size, or that the run still lacks; the caller discards the writes.
pub(super) fn restore_held_values(
    transaction: &mut FlowTransaction<'_>,
    cursor: &RunCursor,
    result: &TimeTravelResult,
) -> Result<Result<(), CommandReply>, ApplyError> {
    let rows = cursor.rows;
    let mut staged_ids = BTreeSet::new();
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
        let reference = ValueReference {
            run_id: rows.run_id,
            value_id,
        };
        if !staged_ids.insert(value_id) {
            return Ok(Err(value_rejection(format!(
                "stages value {reference} twice"
            ))));
        }
        let birth: Option<ValueBirthRecord> = transaction.get_message(
            ColumnFamily::Flow,
            &value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id),
            "value birth",
        )?;
        let Some(birth) = birth else {
            return Ok(Err(value_rejection(format!(
                "stages value {reference}, which the run has no birth record of"
            ))));
        };
        let size_bytes = encoded_bytes(value.encoded_len());
        if size_bytes != birth.size_bytes {
            return Ok(Err(value_rejection(format!(
                "stages value {reference} with {size_bytes} bytes, but the run recorded {} bytes",
                birth.size_bytes
            ))));
        }
        if !is_stored_value(transaction, rows, value_id)? {
            restore_unheld_value(transaction, rows, value_id, &birth, value, cursor.seq);
        }
    }
    let mut missing = BTreeSet::new();
    let newly_held = newly_held_values(
        transaction,
        rows,
        &cursor.record,
        &result.rows,
        &result.run_status,
        HeldValueScan {
            memory_budget: &ReplayMemoryBudget::unlimited(),
            live_bytes: 0,
        },
    )?
    .ok_or_else(|| ApplyError::InvariantViolation {
        detail: "an unlimited memory budget was exceeded".to_owned(),
    })?;
    let input_value_ids = reapplied_input_value_ids(&result.reapplied_inputs, rows);
    for value_id in newly_held.into_keys().chain(input_value_ids) {
        if !is_stored_value(transaction, rows, value_id)? {
            missing.insert(value_id);
        }
    }
    if missing.is_empty() {
        return Ok(Ok(()));
    }
    let missing: Vec<String> = missing
        .into_iter()
        .map(|value_id| {
            ValueReference {
                run_id: rows.run_id,
                value_id,
            }
            .to_string()
        })
        .collect();
    Ok(Err(value_rejection(format!(
        "lacks values that the restored state holds or its reapplied input references, and the \
         run no longer stores: {}",
        missing.join(", ")
    ))))
}

fn value_rejection(detail: String) -> CommandReply {
    error_reply(
        grpc_code::ABORTED,
        ErrorSubStatus::Uncategorized,
        format!("the staged time travel result {detail}"),
    )
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
) -> Result<Result<Vec<TimeTravelStagedInput>, CommandReply>, ApplyError> {
    let last_seq = cursor.seq.saturating_sub(1);
    if computed_through_seq > last_seq {
        return Ok(Err(stale_result_reply(format!(
            "it covers run seq {computed_through_seq}, after the run's last seq {last_seq}"
        ))));
    }
    let history = LocalHistorySource::new(transaction, cursor.rows);
    match collect_external_inputs(
        &history,
        lineage,
        computed_through_seq + 1..=last_seq,
        cursor.record.window_start_seq,
        &ReplayMemoryBudget::unlimited(),
    )? {
        Ok(inputs) => Ok(Ok(inputs)),
        Err(InputCollectionFailure::MissingRecord(missing_seq)) => {
            Ok(Err(stale_result_reply(format!(
                "the run no longer stores the history record at run seq {missing_seq} \
                 locally, whose external input would be reapplied"
            ))))
        }
        Err(InputCollectionFailure::MemoryExceeded) => Err(ApplyError::InvariantViolation {
            detail: "an unlimited memory budget was exceeded".to_owned(),
        }),
    }
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
