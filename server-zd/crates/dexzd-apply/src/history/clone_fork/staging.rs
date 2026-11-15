// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CloneForkFlow stagings: the Stage command, chunk rows, the partition cap, and expiry.
//!
//! A staging belongs to the new Flow, not to a run, since the Flow may have none.

use dexzd_keyspace::row_keys::{
    flow_clone_fork_staging_chunk_key, flow_clone_fork_staging_index_key,
    flow_clone_fork_staging_key, prefix_range,
};
use dexzd_keyspace::{ColumnFamily, IndexKind, partition_index_prefix};
use dexzd_protocol::dex::{ErrorSubStatus, FlowStatus};
use dexzd_protocol::dexzd::v1::{
    CheckpointRow, CommandReply, FlowCloneForkStageCommand, FlowCloneForkStagedChunk,
    FlowCloneForkStagedTarget, FlowCloneForkStagingExpireCommand, FlowCloneForkStagingIndexRecord,
    FlowCloneForkStagingRecord, TimeTravelStagedChildFlow, TimeTravelStagedValue,
};
use prost::Message;

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::history::attempt_scope::hex;
use crate::history::time_travel::{
    IDLE_STAGING_MICROS, MAX_STAGE_CHUNK_BYTES, STAGE_CHUNK_TARGET_BYTES, entry_bytes,
    validate_staged_child_flow, validate_staged_row,
};
use crate::state_access::FlowTransaction;
use crate::values::held_table_values;

/// Bytes of a staging ID, which the owner derives from the Flow ID and the request ID.
pub(crate) const STAGING_ID_BYTES: usize = 16;

/// Stagings that one partition holds at most at once; another one is rejected.
const MAX_PARTITION_STAGINGS: usize = 64;

/// Chunk bytes that one partition's stagings hold at most: about 64 copies at the default
/// 64 MiB state limit.
const MAX_PARTITION_STAGED_BYTES: u64 = 4 * 1024 * 1024 * 1024;

/// Applies one FlowCloneForkStage chunk. It records no history and takes no run seq.
///
/// A malformed chunk is rejected before it is stored, so FlowCloneFork never reads one.
pub(crate) fn apply_flow_clone_fork_stage(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &FlowCloneForkStageCommand,
) -> Result<HandlerResult, ApplyError> {
    if let Err(reply) = validate_stage_command(command) {
        return Ok(HandlerResult::Rejected(reply));
    }
    let staging_id = command.staging_id.as_slice();
    let existing = read_staging_record(transaction, staging_id)?;
    let partition = partition_stagings(transaction)?;
    let staging = match (&existing, command.chunk_index) {
        (Some(_), 0) => FlowCloneForkStagingRecord::default(),
        (None, 0) => {
            if partition.count >= MAX_PARTITION_STAGINGS {
                return Ok(HandlerResult::Rejected(error_reply(
                    grpc_code::RESOURCE_EXHAUSTED,
                    ErrorSubStatus::Uncategorized,
                    format!(
                        "this partition already holds {MAX_PARTITION_STAGINGS} clone fork \
                         stagings in progress; retry the clone fork later"
                    ),
                )));
            }
            FlowCloneForkStagingRecord::default()
        }
        (Some(record), chunk_index) => {
            if record.is_complete || record.chunk_count != chunk_index {
                return Ok(HandlerResult::Rejected(out_of_order_chunk_reply(
                    command, record,
                )));
            }
            *record
        }
        (None, chunk_index) => {
            return Ok(HandlerResult::Rejected(error_reply(
                grpc_code::ABORTED,
                ErrorSubStatus::Uncategorized,
                format!(
                    "clone fork staging {} is not in progress, so it cannot take chunk \
                     {chunk_index}",
                    hex(staging_id)
                ),
            )));
        }
    };
    let chunk_bytes = u64::try_from(command.chunk.len()).expect("a chunk length fits a u64");
    let staged_bytes = staging.staged_bytes.saturating_add(chunk_bytes);
    let other_stagings_bytes = partition
        .staged_bytes
        .saturating_sub(existing.as_ref().map_or(0, |record| record.staged_bytes));
    if other_stagings_bytes.saturating_add(staged_bytes) > MAX_PARTITION_STAGED_BYTES {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::RESOURCE_EXHAUSTED,
            ErrorSubStatus::Uncategorized,
            format!(
                "chunk {} of clone fork staging {} would make this partition's stagings in \
                 progress hold more than {MAX_PARTITION_STAGED_BYTES} bytes; retry the clone \
                 fork later",
                command.chunk_index,
                hex(staging_id)
            ),
        )));
    }
    match (&existing, command.chunk_index) {
        (Some(record), 0) => delete_staging_rows(transaction, staging_id, record),
        (Some(record), _) => {
            delete_staging_index_row(transaction, staging_id, record.staged_at_micros);
        }
        (None, _) => {}
    }
    let flow_hash = transaction.context().flow_hash;
    transaction.put(
        ColumnFamily::Value,
        flow_clone_fork_staging_chunk_key(flow_hash, staging_id, command.chunk_index),
        command.chunk.clone(),
    );
    transaction.put_message(
        ColumnFamily::Flow,
        flow_clone_fork_staging_key(flow_hash, staging_id),
        &FlowCloneForkStagingRecord {
            chunk_count: staging.chunk_count + 1,
            is_complete: command.is_last_chunk,
            staged_at_micros: scope.now_micros,
            staged_bytes,
        },
    );
    transaction.put_message(
        ColumnFamily::PartitionIndex,
        flow_clone_fork_staging_index_key(
            transaction.context().partition,
            scope.now_micros,
            flow_hash,
            staging_id,
        ),
        &FlowCloneForkStagingIndexRecord {
            tenant_id: scope.tenant_id.0,
            flow_id: scope.flow_id().to_owned(),
            staged_bytes,
        },
    );
    Ok(HandlerResult::Applied(no_run_output(ok_reply())))
}

/// Deletes the named staging when no chunk extended it for 10 minutes of command time.
///
/// It records no history and takes no run seq.
pub(crate) fn apply_flow_clone_fork_staging_expire(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &FlowCloneForkStagingExpireCommand,
) -> Result<HandlerResult, ApplyError> {
    let Some(record) = read_staging_record(transaction, &command.staging_id)? else {
        return Ok(HandlerResult::Rejected(ok_reply()));
    };
    let idle_micros = scope.now_micros.saturating_sub(record.staged_at_micros);
    if idle_micros < IDLE_STAGING_MICROS {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    delete_staging_rows(transaction, &command.staging_id, &record);
    Ok(HandlerResult::Applied(no_run_output(ok_reply())))
}

/// The output of a command that changed no run.
pub(super) fn no_run_output(reply: CommandReply) -> HandlerOutput {
    HandlerOutput {
        reply,
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    }
}

/// Returns every chunk of the complete staging `staging_id` in chunk order, or `None` when the
/// Flow has no such staging or it is incomplete.
///
/// Returns an error when a chunk row is missing or does not decode.
pub(super) fn read_complete_staging(
    transaction: &FlowTransaction<'_>,
    staging_id: &[u8],
) -> Result<Option<Vec<FlowCloneForkStagedChunk>>, ApplyError> {
    let Some(record) = read_staging_record(transaction, staging_id)? else {
        return Ok(None);
    };
    if !record.is_complete {
        return Ok(None);
    }
    let flow_hash = transaction.context().flow_hash;
    let chunks = (0..record.chunk_count)
        .map(|chunk_index| {
            let key = flow_clone_fork_staging_chunk_key(flow_hash, staging_id, chunk_index);
            transaction
                .get_message::<FlowCloneForkStagedChunk>(
                    ColumnFamily::Value,
                    &key,
                    "clone fork staging",
                )?
                .ok_or_else(|| ApplyError::CorruptState {
                    table: "clone fork staging",
                    key: hex(&key),
                    detail: format!(
                        "staging {} lists {} chunks but chunk {chunk_index} is missing",
                        hex(staging_id),
                        record.chunk_count
                    ),
                })
        })
        .collect::<Result<Vec<_>, _>>()?;
    Ok(Some(chunks))
}

/// Deletes the staging `staging_id` of the command's Flow, if any.
///
/// Returns whether a staging existed.
pub(super) fn delete_staging(
    transaction: &mut FlowTransaction<'_>,
    staging_id: &[u8],
) -> Result<bool, ApplyError> {
    let Some(record) = read_staging_record(transaction, staging_id)? else {
        return Ok(false);
    };
    delete_staging_rows(transaction, staging_id, &record);
    Ok(true)
}

fn read_staging_record(
    transaction: &FlowTransaction<'_>,
    staging_id: &[u8],
) -> Result<Option<FlowCloneForkStagingRecord>, ApplyError> {
    transaction.get_message(
        ColumnFamily::Flow,
        &flow_clone_fork_staging_key(transaction.context().flow_hash, staging_id),
        "clone fork staging record",
    )
}

fn delete_staging_rows(
    transaction: &mut FlowTransaction<'_>,
    staging_id: &[u8],
    record: &FlowCloneForkStagingRecord,
) {
    let flow_hash = transaction.context().flow_hash;
    for chunk_index in 0..record.chunk_count {
        transaction.delete(
            ColumnFamily::Value,
            flow_clone_fork_staging_chunk_key(flow_hash, staging_id, chunk_index),
        );
    }
    transaction.delete(
        ColumnFamily::Flow,
        flow_clone_fork_staging_key(flow_hash, staging_id),
    );
    delete_staging_index_row(transaction, staging_id, record.staged_at_micros);
}

fn delete_staging_index_row(
    transaction: &mut FlowTransaction<'_>,
    staging_id: &[u8],
    staged_at_micros: i64,
) {
    let context = transaction.context();
    transaction.delete(
        ColumnFamily::PartitionIndex,
        flow_clone_fork_staging_index_key(
            context.partition,
            staged_at_micros,
            context.flow_hash,
            staging_id,
        ),
    );
}

/// The stagings in progress on one partition.
struct PartitionStagings {
    count: usize,
    staged_bytes: u64,
}

/// Counts the partition's stagings, up to [`MAX_PARTITION_STAGINGS`], and sums their bytes.
///
/// Returns an error when an index row does not decode.
fn partition_stagings(transaction: &FlowTransaction<'_>) -> Result<PartitionStagings, ApplyError> {
    let prefix = partition_index_prefix(
        transaction.context().partition,
        IndexKind::FlowCloneForkStaging,
    );
    let (start, end) = prefix_range(&prefix);
    let index_rows = transaction.scan(
        ColumnFamily::PartitionIndex,
        &start,
        &end,
        MAX_PARTITION_STAGINGS,
    )?;
    let mut staged_bytes: u64 = 0;
    for (key, value) in &index_rows {
        let index = FlowCloneForkStagingIndexRecord::decode(value.as_slice()).map_err(|error| {
            ApplyError::CorruptState {
                table: "clone fork staging index",
                key: hex(key),
                detail: error.to_string(),
            }
        })?;
        staged_bytes = staged_bytes.saturating_add(index.staged_bytes);
    }
    Ok(PartitionStagings {
        count: index_rows.len(),
        staged_bytes,
    })
}

/// Checks a chunk's ID, size, decoding, target placement, and entries before storing it.
///
/// Returns the INVALID_ARGUMENT reply for a malformed chunk.
fn validate_stage_command(command: &FlowCloneForkStageCommand) -> Result<(), CommandReply> {
    if command.staging_id.len() != STAGING_ID_BYTES {
        return Err(invalid_staging_reply(&format!(
            "the staging ID has {} bytes instead of {STAGING_ID_BYTES}",
            command.staging_id.len()
        )));
    }
    if command.chunk.len() > MAX_STAGE_CHUNK_BYTES {
        return Err(invalid_staging_reply(&format!(
            "chunk {} has {} bytes, more than the {MAX_STAGE_CHUNK_BYTES} one command carries",
            command.chunk_index,
            command.chunk.len()
        )));
    }
    let chunk = FlowCloneForkStagedChunk::decode(command.chunk.as_slice()).map_err(|error| {
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
    let detail = match &chunk.target {
        Some(target) => validate_staged_target(target),
        None => Ok(()),
    }
    .and_then(|()| validate_staged_entries(&chunk));
    detail.map_err(|detail| {
        invalid_staging_reply(&format!("chunk {}: {detail}", command.chunk_index))
    })
}

/// Checks the target of chunk 0.
///
/// Returns what is malformed.
fn validate_staged_target(target: &FlowCloneForkStagedTarget) -> Result<(), String> {
    if target.request.is_none() || target.source.is_none() {
        return Err("the target has no request or no source".to_owned());
    }
    if target.source_run_seq == 0 || target.first_run_seq <= target.source_run_seq {
        return Err(format!(
            "the target's first run seq {} does not follow its source run seq {}",
            target.first_run_seq, target.source_run_seq
        ));
    }
    if target.flow_type.is_empty() {
        return Err("the target has no Flow type".to_owned());
    }
    let is_running = target
        .run_status
        .as_ref()
        .is_some_and(|status| status.status == FlowStatus::Running as i32);
    if !is_running {
        return Err("the target's run status is not RUNNING".to_owned());
    }
    Ok(())
}

/// Checks the rows, values, and child Flow findings of one chunk.
///
/// Returns what is malformed.
fn validate_staged_entries(chunk: &FlowCloneForkStagedChunk) -> Result<(), String> {
    for row in &chunk.rows {
        validate_staged_row(row)?;
    }
    held_table_values(&chunk.rows).map_err(|error| format!("a row is malformed: {error}"))?;
    let mut value_ids = std::collections::BTreeSet::new();
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
    for child_flow in &chunk.child_flows {
        validate_staged_child_flow(child_flow)?;
    }
    Ok(())
}

fn invalid_staging_reply(detail: &str) -> CommandReply {
    error_reply(
        grpc_code::INVALID_ARGUMENT,
        ErrorSubStatus::Uncategorized,
        format!("invalid clone fork staging chunk: {detail}"),
    )
}

fn out_of_order_chunk_reply(
    command: &FlowCloneForkStageCommand,
    record: &FlowCloneForkStagingRecord,
) -> CommandReply {
    error_reply(
        grpc_code::ABORTED,
        ErrorSubStatus::Uncategorized,
        format!(
            "clone fork staging {} holds {} chunks{}, so it cannot take chunk {}",
            hex(&command.staging_id),
            record.chunk_count,
            if record.is_complete {
                " and is complete"
            } else {
                ""
            },
            command.chunk_index
        ),
    )
}

/// What one CloneForkFlow stages: the target in chunk 0, then rows, values, and findings.
pub(super) struct StagedCloneForkParts<'parts> {
    pub(super) target: &'parts FlowCloneForkStagedTarget,
    pub(super) rows: &'parts [CheckpointRow],
    pub(super) values: &'parts [TimeTravelStagedValue],
    pub(super) child_flows: &'parts [TimeTravelStagedChildFlow],
}

/// An entry of a staging is larger than one log record can carry.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error(
    "clone fork cannot stage a {kind} of {size_bytes} bytes; one command carries at most \
     {max_bytes} bytes"
)]
pub struct CloneForkStagedEntryTooLarge {
    /// What the entry is: a row, a value, or a child Flow finding.
    pub kind: &'static str,
    /// Encoded size of the entry.
    pub size_bytes: usize,
    /// Largest entry one FlowCloneForkStage command carries.
    pub max_bytes: usize,
}

/// Splits `parts` into the FlowCloneForkStage commands that stage them under `staging_id`, in
/// chunks of about 8 MiB of entries.
///
/// Returns [`CloneForkStagedEntryTooLarge`] when an entry cannot fit in one command.
pub(super) fn stage_commands(
    staging_id: &[u8],
    parts: &StagedCloneForkParts<'_>,
) -> Result<Vec<FlowCloneForkStageCommand>, CloneForkStagedEntryTooLarge> {
    let mut builder = ChunkBuilder {
        encoded_chunks: Vec::new(),
        current: FlowCloneForkStagedChunk {
            target: Some(parts.target.clone()),
            ..FlowCloneForkStagedChunk::default()
        },
        current_bytes: entry_bytes(parts.target.encoded_len()),
    };
    for row in parts.rows {
        builder.add("row", row.encoded_len(), |chunk| {
            chunk.rows.push(row.clone())
        })?;
    }
    for value in parts.values {
        builder.add("value", value.encoded_len(), |chunk| {
            chunk.values.push(value.clone());
        })?;
    }
    for child_flow in parts.child_flows {
        builder.add("child Flow finding", child_flow.encoded_len(), |chunk| {
            chunk.child_flows.push(child_flow.clone());
        })?;
    }
    let encoded_chunks = builder.finish();
    let chunk_count = encoded_chunks.len();
    Ok(encoded_chunks
        .into_iter()
        .enumerate()
        .map(|(chunk_index, chunk)| FlowCloneForkStageCommand {
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
    current: FlowCloneForkStagedChunk,
    current_bytes: usize,
}

impl ChunkBuilder {
    fn add(
        &mut self,
        kind: &'static str,
        message_bytes: usize,
        push: impl FnOnce(&mut FlowCloneForkStagedChunk),
    ) -> Result<(), CloneForkStagedEntryTooLarge> {
        let size_bytes = entry_bytes(message_bytes);
        if size_bytes > MAX_STAGE_CHUNK_BYTES {
            return Err(CloneForkStagedEntryTooLarge {
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
