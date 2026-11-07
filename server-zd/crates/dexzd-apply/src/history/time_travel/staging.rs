// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Staged TimeTravel results: the TimeTravelStage command, its chunk rows, and their cleanup.

use dexzd_keyspace::ColumnFamily;
use dexzd_keyspace::row_keys::time_travel_staging_key;
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{
    CommandReply, TimeTravelStageCommand, TimeTravelStagedChunk, TimeTravelStagedTarget,
    TimeTravelStagingState,
};
use prost::Message;

use super::TimeTravelResult;
use super::result_computation::StagedEntryTooLarge;
use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::history::attempt_scope::{AttemptRow, hex};
use crate::history::state_rebuild::{HistoryPoint, RebuiltAttempt};
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::reconcile_run_storage;

/// Chunks stop growing past this size, far below the 32 MiB log record limit, unless one entry
/// is larger.
const STAGE_CHUNK_TARGET_BYTES: usize = 8 * 1024 * 1024;

/// Largest chunk one Stage command carries: the 32 MiB log record limit minus room for the
/// command around it.
const MAX_STAGE_CHUNK_BYTES: usize = 32 * 1024 * 1024 - 64 * 1024;

/// A staging no chunk extended for this long is abandoned; a later run command deletes it.
const IDLE_STAGING_MICROS: i64 = 10 * 60 * 1_000_000;

/// Applies one TimeTravelStage chunk. It records no history and takes no run seq.
pub(crate) fn apply_time_travel_stage(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &TimeTravelStageCommand,
) -> Result<HandlerResult, ApplyError> {
    let mut cursor = match RunCursor::open_without_seq(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    if let Err(reply) = validate_stage_command(command) {
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

/// Rejects a chunk that does not decode, and a target outside chunk 0 or missing from it.
fn validate_stage_command(command: &TimeTravelStageCommand) -> Result<(), CommandReply> {
    let invalid = |detail: String| {
        error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            format!("invalid time travel staging chunk: {detail}"),
        )
    };
    if command.staging_id.is_empty() {
        return Err(invalid("the staging ID is empty".to_owned()));
    }
    let chunk = TimeTravelStagedChunk::decode(command.chunk.as_slice())
        .map_err(|error| invalid(format!("the chunk does not decode: {error}")))?;
    if chunk.target.is_some() != (command.chunk_index == 0) {
        return Err(invalid(format!(
            "chunk {} must carry the target exactly when it is chunk 0",
            command.chunk_index
        )));
    }
    Ok(())
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

/// Splits `result` into the TimeTravelStage commands that stage it under `staging_id`.
///
/// Returns [`StagedEntryTooLarge`] when an entry cannot fit in one command.
pub(super) fn staged_chunks(
    staging_id: &[u8],
    result: &TimeTravelResult,
) -> Result<Vec<TimeTravelStageCommand>, StagedEntryTooLarge> {
    let target = TimeTravelStagedTarget {
        selected_run_attempt: result.selected_attempt,
        run_attempt: result.target.point.attempt,
        run_seq: result.target.point.seq,
        last_event_id: result.target.last_event_id,
        close_history_message: result.target.close_history_message.clone(),
        run_status: Some(result.run_status.clone()),
    };
    let mut builder = ChunkBuilder {
        chunks: Vec::new(),
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
    let chunks = builder.finish();
    let chunk_count = chunks.len();
    Ok(chunks
        .into_iter()
        .enumerate()
        .map(|(chunk_index, chunk)| TimeTravelStageCommand {
            staging_id: staging_id.to_vec(),
            chunk_index: u32::try_from(chunk_index).expect("a staging has fewer than 2^32 chunks"),
            is_last_chunk: chunk_index + 1 == chunk_count,
            chunk: chunk.encode_to_vec(),
        })
        .collect())
}

/// Packs entries into chunks of about [`STAGE_CHUNK_TARGET_BYTES`], in order.
struct ChunkBuilder {
    chunks: Vec<TimeTravelStagedChunk>,
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
            self.chunks.push(std::mem::take(&mut self.current));
            self.current_bytes = 0;
        }
        push(&mut self.current);
        self.current_bytes += size_bytes;
        Ok(())
    }

    fn finish(mut self) -> Vec<TimeTravelStagedChunk> {
        self.chunks.push(self.current);
        self.chunks
    }
}

/// Encoded size of a length-delimited entry of a chunk: its field key, length, and message.
fn entry_bytes(message_bytes: usize) -> usize {
    1 + prost::length_delimiter_len(message_bytes) + message_bytes
}
