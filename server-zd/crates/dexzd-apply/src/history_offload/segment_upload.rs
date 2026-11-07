// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The upload acknowledgment of a history segment (plan section 5.5) and the uploaded seq U
//! that bounds window pruning.

use dexzd_keyspace::row_keys::{
    history_segment_key, history_upload_pending_index_key, prefix_range,
};
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dex::ErrorSubStatus;
use dexzd_protocol::dexzd::v1::{
    HistorySegmentRecord, HistorySegmentState, HistorySegmentUploadedCommand,
};

use crate::ApplyError;
use crate::command_handlers::command_replies::{error_reply, grpc_code, ok_reply};
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::history_offload::offload_mode::is_history_offload_enabled;
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::reconcile_run_storage;

const CONTENT_CHECKSUM_BYTES: usize = 16;

/// Marks the run's oldest SEALED segment UPLOADED and advances the run's uploaded seq U to its
/// last seq. Removes the run's upload pending index row once no SEALED segment remains.
///
/// The command records no history, takes no run seq, and is not bound to an attempt. Replies OK
/// without writes when the acknowledgment names another segment, as a duplicate or stale one
/// does, NOT_FOUND when the run is gone or purging, and INVALID_ARGUMENT without writes when the
/// checksum is not 16 bytes or the object size is zero.
pub(crate) fn apply_history_segment_uploaded(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &HistorySegmentUploadedCommand,
) -> Result<HandlerResult, ApplyError> {
    let mut cursor = match RunCursor::open_without_seq(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let invalid_detail = if command.content_checksum.len() != CONTENT_CHECKSUM_BYTES {
        Some(format!(
            "history segment content checksum has {} bytes instead of {CONTENT_CHECKSUM_BYTES}",
            command.content_checksum.len()
        ))
    } else if command.object_size_bytes == 0 {
        Some("history segment object size is zero".to_owned())
    } else {
        None
    };
    if let Some(detail) = invalid_detail {
        return Ok(HandlerResult::Rejected(error_reply(
            grpc_code::INVALID_ARGUMENT,
            ErrorSubStatus::Uncategorized,
            detail,
        )));
    }
    let rows = cursor.rows;
    let uploaded_through_seq = cursor.record.history_uploaded_through_seq;
    let sealed = sealed_history_segments(transaction, rows, uploaded_through_seq, 2)?;
    let Some(oldest) = sealed.first() else {
        return Ok(HandlerResult::Rejected(ok_reply()));
    };
    if oldest.first_seq != command.first_seq || oldest.last_seq != command.last_seq {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    let uploaded = HistorySegmentRecord {
        state: HistorySegmentState::Uploaded as i32,
        content_checksum: command.content_checksum.clone(),
        object_size_bytes: command.object_size_bytes,
        ..oldest.clone()
    };
    transaction.put_message(
        ColumnFamily::Flow,
        history_segment_key(rows.flow_hash, rows.run_id, uploaded.first_seq),
        &uploaded,
    );
    cursor.record.history_uploaded_through_seq = uploaded.last_seq;
    if sealed.len() == 1 {
        let partition = transaction.context().partition;
        transaction.delete(
            ColumnFamily::PartitionIndex,
            history_upload_pending_index_key(partition, rows.flow_hash, rows.run_id),
        );
    }
    crate::history::delete_idle_staging(transaction, &mut cursor, scope.now_micros);
    reconcile_run_storage(transaction, &mut cursor)?;
    Ok(HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: Vec::new(),
        run_id: None,
        replayed: None,
    }))
}

/// Returns up to `limit` SEALED segments of the run, oldest first.
///
/// Segments are acknowledged in seq order, so every segment that starts after the uploaded seq
/// is SEALED and every other one is not.
pub(crate) fn sealed_history_segments(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    uploaded_through_seq: u64,
    limit: usize,
) -> Result<Vec<HistorySegmentRecord>, ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistorySegment);
    let (_, end) = prefix_range(&prefix);
    let start = history_segment_key(
        rows.flow_hash,
        rows.run_id,
        uploaded_through_seq.saturating_add(1),
    );
    let segments = transaction.scan_messages::<HistorySegmentRecord>(
        ColumnFamily::Flow,
        &start,
        &end,
        limit,
        "history segment",
    )?;
    segments
        .into_iter()
        .map(|(_, segment)| {
            if segment.state == HistorySegmentState::Sealed as i32 {
                Ok(segment)
            } else {
                Err(ApplyError::InvariantViolation {
                    detail: format!(
                        "history segment {}..={} after uploaded seq {uploaded_through_seq} is not \
                         sealed",
                        segment.first_seq, segment.last_seq
                    ),
                })
            }
        })
        .collect()
}

/// Returns the first seq window pruning keeps, so rows are deleted only once uploaded. Pruning
/// stops after the run's uploaded seq while the partition offloads history and, whatever the
/// mode, while the run has a SEALED segment, so sealed content stays until its acknowledgment.
/// Returns `u64::MAX` otherwise, which leaves pruning unchanged.
pub(crate) fn first_seq_kept_until_upload(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<u64, ApplyError> {
    let record = &cursor.record;
    let uploaded_through_seq = record.history_uploaded_through_seq;
    let is_fenced = is_history_offload_enabled(transaction)?
        || (record.history_open_segment_first_seq != 0
            && !sealed_history_segments(transaction, cursor.rows, uploaded_through_seq, 1)?
                .is_empty());
    Ok(if is_fenced {
        uploaded_through_seq.saturating_add(1)
    } else {
        u64::MAX
    })
}
