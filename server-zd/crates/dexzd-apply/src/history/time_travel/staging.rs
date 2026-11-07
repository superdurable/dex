// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Staged TimeTravel results: the TimeTravelStage command, its chunk rows, and their cleanup.

use std::collections::BTreeSet;

use dexzd_keyspace::row_keys::time_travel_staging_key;
use dexzd_keyspace::{ColumnFamily, RunTable};
use dexzd_protocol::dex::{ErrorSubStatus, FlowStatus, ResetFlowRequest};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, ChannelMessageRecord, ChannelMetaRecord, ChannelWaiterRecord, CheckpointRow,
    CommandReply, LockRecord, RunRecord, StepCounterRecord, StepExecutionRecord,
    TimeTravelStageCommand, TimeTravelStagedChunk, TimeTravelStagedTarget, TimeTravelStagingState,
    TimerId, TimerRecord,
};
use prost::Message;

use super::TimeTravelResult;
use super::result_computation::{StagedEntryTooLarge, is_reapplied_input};
use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::history::attempt_scope::{ATTEMPT_SCOPED_TABLES, AttemptRow, hex, holder_rows};
use crate::history::state_rebuild::{HistoryPoint, RebuiltAttempt, encoded_bytes};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::{held_table_values, reconcile_run_storage};

/// Chunks stop growing past this size, far below the 32 MiB log record limit, unless one entry
/// is larger.
const STAGE_CHUNK_TARGET_BYTES: usize = 8 * 1024 * 1024;

/// Largest chunk one Stage command carries: the 32 MiB log record limit minus room for the
/// command around it.
const MAX_STAGE_CHUNK_BYTES: usize = 32 * 1024 * 1024 - 64 * 1024;

/// A staging no chunk extended for this long is abandoned; a later run command deletes it.
const IDLE_STAGING_MICROS: i64 = 10 * 60 * 1_000_000;

/// Applies one TimeTravelStage chunk. It records no history and takes no run seq.
///
/// A malformed chunk is rejected before anything is stored, so a later TimeTravel never reads
/// one.
pub(crate) fn apply_time_travel_stage(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &TimeTravelStageCommand,
) -> Result<HandlerResult, ApplyError> {
    let mut cursor = match RunCursor::open_without_seq(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    if let Err(reply) = validate_stage_command(command, &cursor.record) {
        return Ok(HandlerResult::Rejected(reply));
    }
    let staging = match cursor.record.time_travel_staging.take() {
        Some(staging) if staging.staging_id == command.staging_id => {
            if staging.is_complete || staging.chunk_count != command.chunk_index {
                return Ok(HandlerResult::Rejected(out_of_order_chunk_reply(
                    command, &staging,
                )));
            }
            staging
        }
        other => {
            if command.chunk_index != 0 {
                return Ok(HandlerResult::Rejected(error_reply(
                    grpc_code::ABORTED,
                    ErrorSubStatus::Uncategorized,
                    format!(
                        "time travel staging {} is not in progress, so it cannot take chunk {}",
                        hex(&command.staging_id),
                        command.chunk_index
                    ),
                )));
            }
            if let Some(replaced) = other {
                delete_staging_rows(transaction, cursor.rows, &replaced);
            }
            TimeTravelStagingState {
                staging_id: command.staging_id.clone(),
                ..TimeTravelStagingState::default()
            }
        }
    };
    transaction.put(
        ColumnFamily::Value,
        time_travel_staging_key(
            cursor.rows.flow_hash,
            cursor.rows.run_id,
            command.chunk_index,
        ),
        command.chunk.clone(),
    );
    cursor.record.time_travel_staging = Some(TimeTravelStagingState {
        chunk_count: staging.chunk_count + 1,
        is_complete: command.is_last_chunk,
        staged_at_micros: scope.now_micros,
        ..staging
    });
    reconcile_run_storage(transaction, &mut cursor)?;
    Ok(HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: Some(cursor.run_id()),
        replayed: None,
    }))
}

/// Checks a chunk before storing it: it decodes, only chunk 0 has a target, and entries are
/// well formed.
///
/// Returns ABORTED when the run attempt changed since the computation, and INVALID_ARGUMENT for
/// a malformed chunk.
fn validate_stage_command(
    command: &TimeTravelStageCommand,
    record: &RunRecord,
) -> Result<(), CommandReply> {
    if command.staging_id.is_empty() {
        return Err(invalid_staging_reply("the staging ID is empty"));
    }
    let chunk = TimeTravelStagedChunk::decode(command.chunk.as_slice()).map_err(|error| {
        invalid_staging_reply(&format!(
            "chunk {} does not decode: {error}",
            command.chunk_index
        ))
    })?;
    if chunk.target.is_some() != (command.chunk_index == 0) {
        return Err(invalid_staging_reply(&format!(
            "chunk {} must carry the target exactly when it is chunk 0",
            command.chunk_index
        )));
    }
    if let Some(target) = &chunk.target {
        validate_staged_target(target, record)?;
    }
    validate_staged_entries(&chunk).map_err(|detail| {
        invalid_staging_reply(&format!("chunk {}: {detail}", command.chunk_index))
    })
}

/// Checks the target of chunk 0 against the run it stages for.
fn validate_staged_target(
    target: &TimeTravelStagedTarget,
    record: &RunRecord,
) -> Result<(), CommandReply> {
    if target.expected_run_attempt != record.current_attempt {
        return Err(error_reply(
            grpc_code::ABORTED,
            ErrorSubStatus::Uncategorized,
            format!(
                "the time travel result was computed at run attempt {}, but the run is at run \
                 attempt {}",
                target.expected_run_attempt, record.current_attempt
            ),
        ));
    }
    let invalid = |detail: String| Err(invalid_staging_reply(&format!("the target {detail}")));
    if target.request.is_none() {
        return invalid("has no request".to_owned());
    }
    let last_seq = record.next_run_seq.saturating_sub(1);
    if target.selected_run_attempt == 0 || target.selected_run_attempt > record.current_attempt {
        return invalid(format!(
            "selects run attempt {} of attempts 1 to {}",
            target.selected_run_attempt, record.current_attempt
        ));
    }
    if target.run_attempt == 0 || target.run_attempt > target.selected_run_attempt {
        return invalid(format!(
            "point is in run attempt {}, outside the lineage of run attempt {}",
            target.run_attempt, target.selected_run_attempt
        ));
    }
    if target.run_seq == 0
        || target.run_seq > target.computed_through_seq
        || target.computed_through_seq > last_seq
    {
        return invalid(format!(
            "point at run seq {} and computed seq {} do not fit the run's seqs 1 to {last_seq}",
            target.run_seq, target.computed_through_seq
        ));
    }
    let Some(run_status) = &target.run_status else {
        return invalid("has no run status".to_owned());
    };
    let is_known_status = FlowStatus::try_from(run_status.status)
        .is_ok_and(|status| status != FlowStatus::Unspecified);
    if !is_known_status {
        return invalid(format!("has run status {}", run_status.status));
    }
    held_table_values(&holder_rows(&[], run_status))
        .map_err(|error| invalid_staging_reply(&format!("run status is malformed: {error}")))?;
    Ok(())
}

/// Checks the rows, values, and inputs of one chunk.
///
/// Returns what is malformed.
fn validate_staged_entries(chunk: &TimeTravelStagedChunk) -> Result<(), String> {
    for row in &chunk.rows {
        validate_staged_row(row)?;
    }
    held_table_values(&chunk.rows).map_err(|error| format!("a row is malformed: {error}"))?;
    let mut value_ids = BTreeSet::new();
    for value in &chunk.values {
        let (Some(value_id), Some(_)) = (value.value_id, &value.value) else {
            return Err("a staged value has no ID or no value".to_owned());
        };
        if !value_ids.insert((value_id.run_seq, value_id.index_in_command)) {
            return Err(format!(
                "value ({}, {}) is staged twice",
                value_id.run_seq, value_id.index_in_command
            ));
        }
    }
    let mut previous_position = None;
    for input in &chunk.reapplied_inputs {
        if !input.command.as_ref().is_some_and(is_reapplied_input) {
            return Err(format!(
                "the input of run seq {} is not a reapplied kind of command",
                input.run_seq
            ));
        }
        let position = (input.run_seq, input.index_in_record);
        if input.run_seq == 0 || previous_position.is_some_and(|previous| previous >= position) {
            return Err(format!(
                "the input at run seq {} index {} is out of order",
                input.run_seq, input.index_in_record
            ));
        }
        previous_position = Some(position);
    }
    Ok(())
}

/// Checks that `row` belongs to an attempt-scoped table, has a key, and decodes as its record.
fn validate_staged_row(row: &CheckpointRow) -> Result<(), String> {
    let table = u8::try_from(row.table)
        .ok()
        .and_then(|byte| RunTable::from_byte(byte).ok())
        .filter(|table| ATTEMPT_SCOPED_TABLES.contains(table))
        .ok_or_else(|| format!("a row has table {} outside the attempt", row.table))?;
    if row.key_suffix.is_empty() {
        return Err(format!("a {table:?} row has no key"));
    }
    if !is_staged_row_key(table, &row.key_suffix) {
        return Err(format!("a {table:?} row has a malformed key"));
    }
    let value = row.value.as_slice();
    let decoded = match table {
        RunTable::Attribute => AttributeRecord::decode(value).map(drop),
        RunTable::ChannelMessage => ChannelMessageRecord::decode(value).map(drop),
        RunTable::ChannelMeta => ChannelMetaRecord::decode(value).map(drop),
        RunTable::StepExecution => StepExecutionRecord::decode(value).map(drop),
        RunTable::StepCounter => StepCounterRecord::decode(value).map(drop),
        RunTable::LockTable => LockRecord::decode(value).map(drop),
        RunTable::Waiter => ChannelWaiterRecord::decode(value).map(drop),
        RunTable::Timer => TimerRecord::decode(value).map(drop),
        other => return Err(format!("a row has table {other:?} outside the attempt")),
    };
    decoded.map_err(|error| format!("a {table:?} row does not decode: {error}"))
}

/// Whether `key` has the key suffix layout of `table`.
fn is_staged_row_key(table: RunTable, key: &[u8]) -> bool {
    match table {
        RunTable::Attribute
        | RunTable::ChannelMeta
        | RunTable::StepExecution
        | RunTable::StepCounter
        | RunTable::LockTable => std::str::from_utf8(key).is_ok(),
        RunTable::ChannelMessage => after_channel_name(key).is_some_and(|seq| seq.len() == 8),
        RunTable::Waiter => after_channel_name(key)
            .is_some_and(|step_execution_id| std::str::from_utf8(step_execution_id).is_ok()),
        RunTable::Timer => TimerId::decode(key).is_ok(),
        _ => false,
    }
}

/// Returns the bytes after the length-prefixed UTF-8 Channel name that starts `key`.
fn after_channel_name(key: &[u8]) -> Option<&[u8]> {
    let (length, rest) = key.split_first_chunk::<2>()?;
    let (channel_name, rest) = rest.split_at_checked(usize::from(u16::from_be_bytes(*length)))?;
    std::str::from_utf8(channel_name).ok()?;
    Some(rest)
}

fn invalid_staging_reply(detail: &str) -> CommandReply {
    error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        format!("invalid time travel staging chunk: {detail}"),
    )
}

fn out_of_order_chunk_reply(
    command: &TimeTravelStageCommand,
    staging: &TimeTravelStagingState,
) -> CommandReply {
    error_reply(
        grpc_code::ABORTED,
        ErrorSubStatus::Uncategorized,
        format!(
            "time travel staging {} holds {} chunks{}, so it cannot take chunk {}",
            hex(&staging.staging_id),
            staging.chunk_count,
            if staging.is_complete {
                " and is complete"
            } else {
                ""
            },
            command.chunk_index
        ),
    )
}

/// Returns the complete staging that `staging_id` names, or the rejection the owner retries.
pub(super) fn complete_staging<'record>(
    cursor: &'record RunCursor,
    staging_id: &[u8],
) -> Result<&'record TimeTravelStagingState, CommandReply> {
    match &cursor.record.time_travel_staging {
        Some(staging) if staging.staging_id == staging_id && staging.is_complete => Ok(staging),
        _ => Err(error_reply(
            grpc_code::ABORTED,
            ErrorSubStatus::Uncategorized,
            format!(
                "the staged time travel result {} is not complete",
                hex(staging_id)
            ),
        )),
    }
}

/// Reads every chunk of `staging` in chunk order.
///
/// Returns an error when a chunk row is missing or does not decode.
pub(super) fn read_staged_chunks(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    staging: &TimeTravelStagingState,
) -> Result<Vec<TimeTravelStagedChunk>, ApplyError> {
    (0..staging.chunk_count)
        .map(|chunk_index| {
            let key = time_travel_staging_key(rows.flow_hash, rows.run_id, chunk_index);
            transaction
                .get_message::<TimeTravelStagedChunk>(
                    ColumnFamily::Value,
                    &key,
                    "time travel staging",
                )?
                .ok_or_else(|| ApplyError::CorruptState {
                    table: "time travel staging",
                    key: hex(&key),
                    detail: format!(
                        "staging {} lists {} chunks but chunk {chunk_index} is missing",
                        hex(&staging.staging_id),
                        staging.chunk_count
                    ),
                })
        })
        .collect()
}

/// Returns the target point and attempt state of the complete staging `staging_id`, or `None`
/// when the run has no such staging. It checks nothing against the run.
///
/// Returns an error when a chunk row is missing or corrupt.
pub(crate) fn staged_attempt_state(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    staging_id: &[u8],
) -> Result<Option<(HistoryPoint, RebuiltAttempt)>, ApplyError> {
    let Ok(staging) = complete_staging(cursor, staging_id) else {
        return Ok(None);
    };
    let mut target = None;
    let mut rows = Vec::new();
    for chunk in read_staged_chunks(transaction, cursor.rows, staging)? {
        target = target.or(chunk.target);
        for row in &chunk.rows {
            rows.push(AttemptRow::from_checkpoint_row(row)?);
        }
    }
    let target = target.ok_or_else(|| ApplyError::InvariantViolation {
        detail: "a staged time travel result has no target".to_owned(),
    })?;
    Ok(Some((
        HistoryPoint {
            attempt: target.run_attempt,
            seq: target.run_seq,
        },
        RebuiltAttempt {
            rows,
            run_status: target.run_status.unwrap_or_default(),
            unstored_values: Vec::new(),
        },
    )))
}

/// Deletes the run's staging, if any.
pub(crate) fn delete_staging(transaction: &mut FlowTransaction<'_>, cursor: &mut RunCursor) {
    if let Some(staging) = cursor.record.time_travel_staging.take() {
        delete_staging_rows(transaction, cursor.rows, &staging);
    }
}

/// Deletes the run's staging when no chunk extended it for 10 minutes of command time.
pub(crate) fn delete_idle_staging(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) {
    let is_idle = cursor
        .record
        .time_travel_staging
        .as_ref()
        .is_some_and(|staging| {
            now_micros.saturating_sub(staging.staged_at_micros) >= IDLE_STAGING_MICROS
        });
    if is_idle {
        delete_staging(transaction, cursor);
    }
}

fn delete_staging_rows(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    staging: &TimeTravelStagingState,
) {
    for chunk_index in 0..staging.chunk_count {
        transaction.delete(
            ColumnFamily::Value,
            time_travel_staging_key(rows.flow_hash, rows.run_id, chunk_index),
        );
    }
}

/// Returns the encoded bytes of the entries that staging `result` writes into its chunks.
pub(super) fn staged_result_bytes(result: &TimeTravelResult) -> u64 {
    let rows = result
        .rows
        .iter()
        .map(|row| entry_bytes(row.to_checkpoint_row().encoded_len()));
    let values = result
        .unstored_values
        .iter()
        .map(|value| entry_bytes(value.encoded_len()));
    let inputs = result
        .reapplied_inputs
        .iter()
        .map(|input| entry_bytes(input.encoded_len()));
    rows.chain(values)
        .chain(inputs)
        .map(encoded_bytes)
        .fold(0, u64::saturating_add)
}

/// Splits `result` into the TimeTravelStage commands that stage it under `staging_id`.
///
/// Chunk 0 records `request`, `expected_run_attempt`, and `computed_through_seq`.
///
/// Returns [`StagedEntryTooLarge`] when an entry cannot fit in one command.
pub(super) fn staged_chunks(
    staging_id: &[u8],
    result: &TimeTravelResult,
    request: &ResetFlowRequest,
    expected_run_attempt: u32,
    computed_through_seq: u64,
) -> Result<Vec<TimeTravelStageCommand>, StagedEntryTooLarge> {
    let target = TimeTravelStagedTarget {
        selected_run_attempt: result.selected_attempt,
        run_attempt: result.target.point.attempt,
        run_seq: result.target.point.seq,
        last_event_id: result.target.last_event_id,
        close_history_message: result.target.close_history_message.clone(),
        run_status: Some(result.run_status.clone()),
        request: Some(request.clone()),
        expected_run_attempt,
        computed_through_seq,
    };
    let mut builder = ChunkBuilder {
        encoded_chunks: Vec::new(),
        current: TimeTravelStagedChunk::default(),
        current_bytes: entry_bytes(target.encoded_len()),
    };
    builder.current.target = Some(target);
    for row in &result.rows {
        let row = row.to_checkpoint_row();
        builder.add("row", row.encoded_len(), |chunk| chunk.rows.push(row))?;
    }
    for value in &result.unstored_values {
        builder.add("value", value.encoded_len(), |chunk| {
            chunk.values.push(value.clone());
        })?;
    }
    for input in &result.reapplied_inputs {
        builder.add("external input", input.encoded_len(), |chunk| {
            chunk.reapplied_inputs.push(input.clone());
        })?;
    }
    let encoded_chunks = builder.finish();
    let chunk_count = encoded_chunks.len();
    Ok(encoded_chunks
        .into_iter()
        .enumerate()
        .map(|(chunk_index, chunk)| TimeTravelStageCommand {
            staging_id: staging_id.to_vec(),
            chunk_index: u32::try_from(chunk_index).expect("a staging has fewer than 2^32 chunks"),
            is_last_chunk: chunk_index + 1 == chunk_count,
            chunk,
        })
        .collect())
}

/// Packs entries into chunks of about [`STAGE_CHUNK_TARGET_BYTES`], in order, and encodes each
/// chunk once it is full.
struct ChunkBuilder {
    encoded_chunks: Vec<Vec<u8>>,
    current: TimeTravelStagedChunk,
    current_bytes: usize,
}

impl ChunkBuilder {
    fn add(
        &mut self,
        kind: &'static str,
        message_bytes: usize,
        push: impl FnOnce(&mut TimeTravelStagedChunk),
    ) -> Result<(), StagedEntryTooLarge> {
        let size_bytes = entry_bytes(message_bytes);
        if size_bytes > MAX_STAGE_CHUNK_BYTES {
            return Err(StagedEntryTooLarge {
                kind,
                size_bytes,
                max_bytes: MAX_STAGE_CHUNK_BYTES,
            });
        }
        if self.current_bytes > 0 && self.current_bytes + size_bytes > STAGE_CHUNK_TARGET_BYTES {
            let full = std::mem::take(&mut self.current);
            self.encoded_chunks.push(full.encode_to_vec());
            self.current_bytes = 0;
        }
        push(&mut self.current);
        self.current_bytes += size_bytes;
        Ok(())
    }

    fn finish(mut self) -> Vec<Vec<u8>> {
        self.encoded_chunks.push(self.current.encode_to_vec());
        self.encoded_chunks
    }
}

/// Encoded size of a length-delimited entry of a chunk: its field key, length, and message.
fn entry_bytes(message_bytes: usize) -> usize {
    1 + prost::length_delimiter_len(message_bytes) + message_bytes
}
