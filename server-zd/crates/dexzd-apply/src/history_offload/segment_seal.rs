// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Sealing the open history segment at the end of a command (plan section 5.2).
//!
//! A segment's range and value list come only from the command and committed state, so every
//! owner seals the same segment with the same content.
//!
//! While the partition does not offload, each command of a run with segment state moves the
//! open segment past itself and seals nothing. The seqs and events of those commands then belong
//! to no segment, and their local rows follow window pruning. So a segment opened after offload
//! is turned on again never spans rows that pruning may have deleted while it was off.

use dexzd_keyspace::row_keys::{
    history_segment_key, history_segment_value_key, history_upload_pending_index_key,
};
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId, run_prefix, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    HistorySegmentRecord, HistorySegmentState, HistorySegmentValueRecord, ValueGarbageRecord,
};
use prost::Message;

use crate::command_handlers::run_cursor::RunCursor;
use crate::history_offload::offload_mode::is_history_offload_enabled;
use crate::state_access::FlowTransaction;
use crate::{ApplyEffect, ApplyError};

/// Segment size used when the run's settings predate `history_segment_target_bytes`.
pub(crate) const DEFAULT_HISTORY_SEGMENT_TARGET_BYTES: u64 = 4 * 1024 * 1024;
/// Bounds the HistorySegmentValue rows one seal writes, so one command stays small.
pub(crate) const MAX_SEGMENT_SUPERSEDED_VALUES: u64 = 10_000;

/// Opens the first history segment of a run that the command creates, when its partition
/// offloads history. Runs created while offload is off keep no segment state.
pub(crate) fn open_first_history_segment(
    transaction: &FlowTransaction<'_>,
    cursor: &mut RunCursor,
) -> Result<(), ApplyError> {
    if !is_history_offload_enabled(transaction)? {
        return Ok(());
    }
    cursor.record.history_open_segment_first_seq = cursor.seq;
    cursor.record.history_open_segment_first_event_id = cursor.record.next_event_id.max(1);
    Ok(())
}

/// Adds this command's history rows and superseded values to the open segment, then seals
/// `[open first seq, command seq]` when it reached the target bytes or the superseded value
/// limit, or when the command closed the run.
///
/// Does nothing for a run without segment state. While the partition does not offload, moves
/// the open segment past the command instead, so the command belongs to no segment.
/// `now_micros` is the command's proposal time.
pub(crate) fn seal_history_segment_if_due(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
    has_closed_run: bool,
) -> Result<(), ApplyError> {
    if cursor.record.history_open_segment_first_seq == 0 {
        return Ok(());
    }
    if !is_history_offload_enabled(transaction)? {
        open_segment_after_command(cursor);
        return Ok(());
    }
    let additions = command_segment_additions(transaction, cursor)?;
    let record = &mut cursor.record;
    record.history_open_segment_bytes = record
        .history_open_segment_bytes
        .saturating_add(additions.bytes);
    record.history_open_segment_superseded_value_count = record
        .history_open_segment_superseded_value_count
        .saturating_add(additions.superseded_values);
    let target_bytes = match record
        .settings
        .unwrap_or_default()
        .history_segment_target_bytes
    {
        0 => DEFAULT_HISTORY_SEGMENT_TARGET_BYTES,
        configured => configured,
    };
    let is_due = has_closed_run
        || record.history_open_segment_bytes >= target_bytes
        || record.history_open_segment_superseded_value_count >= MAX_SEGMENT_SUPERSEDED_VALUES;
    if is_due {
        seal_open_segment(transaction, cursor, now_micros)?;
    }
    Ok(())
}

/// What one command adds to the open segment.
struct SegmentAdditions {
    bytes: u64,
    superseded_values: u64,
}

/// Bytes of the history records and checkpoints the command wrote, plus the values it
/// superseded; a value superseded and revived within the command adds nothing.
fn command_segment_additions(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<SegmentAdditions, ApplyError> {
    let prefix = run_prefix(cursor.rows.flow_hash, cursor.rows.run_id);
    let history_bytes = transaction
        .buffered_puts(ColumnFamily::History)
        .filter(|(key, _)| key.starts_with(&prefix))
        .map(|(key, value)| u64::try_from(key.len() + value.len()).unwrap_or(u64::MAX))
        .fold(0, u64::saturating_add);
    let garbage_prefix = run_table_prefix(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::ValueGarbage,
    );
    let mut additions = SegmentAdditions {
        bytes: history_bytes,
        superseded_values: 0,
    };
    for change in
        transaction.buffered_row_changes_with_prefix(ColumnFamily::Flow, &garbage_prefix)?
    {
        let (None, Some(current)) = (&change.previous, &change.current) else {
            continue;
        };
        let garbage = ValueGarbageRecord::decode(current.as_slice()).map_err(|error| {
            ApplyError::InvariantViolation {
                detail: format!("buffered value garbage row is corrupt: {error}"),
            }
        })?;
        additions.bytes = additions.bytes.saturating_add(garbage.size_bytes);
        additions.superseded_values += 1;
    }
    Ok(additions)
}

/// Writes the SEALED segment, its value list, and the run's upload pending index row, then
/// opens the next segment after this command.
fn seal_open_segment(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<(), ApplyError> {
    let rows = cursor.rows;
    let first_seq = cursor.record.history_open_segment_first_seq;
    let last_seq = cursor.seq;
    let next_event_id = cursor.record.next_event_id.max(1);
    let garbage_prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::ValueGarbage);
    let mut start = garbage_prefix.clone();
    start.extend_from_slice(&first_seq.to_be_bytes());
    let mut end = garbage_prefix.clone();
    end.extend_from_slice(&(last_seq + 1).to_be_bytes());
    // Commands key superseded values at their own seq, so this range lists them completely.
    let superseded = transaction.scan_messages::<ValueGarbageRecord>(
        ColumnFamily::Flow,
        &start,
        &end,
        usize::MAX,
        "value garbage",
    )?;
    let mut superseded_value_count = 0_u64;
    for (garbage_key, garbage) in superseded {
        let (superseded_seq, value_id) = garbage_identity(&garbage_key, &garbage_prefix)?;
        transaction.put_message(
            ColumnFamily::Flow,
            history_segment_value_key(rows.flow_hash, rows.run_id, first_seq, value_id),
            &HistorySegmentValueRecord {
                superseded_seq,
                size_bytes: garbage.size_bytes,
            },
        );
        superseded_value_count += 1;
    }
    transaction.put_message(
        ColumnFamily::Flow,
        history_segment_key(rows.flow_hash, rows.run_id, first_seq),
        &HistorySegmentRecord {
            first_seq,
            last_seq,
            first_event_id: cursor.record.history_open_segment_first_event_id,
            last_event_id: next_event_id - 1,
            state: HistorySegmentState::Sealed as i32,
            sealed_at_micros: now_micros,
            superseded_value_count,
            content_checksum: Vec::new(),
            object_size_bytes: 0,
        },
    );
    let partition = transaction.context().partition;
    transaction.put(
        ColumnFamily::PartitionIndex,
        history_upload_pending_index_key(partition, rows.flow_hash, rows.run_id),
        Vec::new(),
    );
    open_segment_after_command(cursor);
    let run_id = cursor.run_id();
    cursor.add_effect(ApplyEffect::HistorySegmentSealed { run_id });
    Ok(())
}

/// Opens an empty segment whose seqs and events start right after the command's.
fn open_segment_after_command(cursor: &mut RunCursor) {
    let first_seq = cursor.seq + 1;
    let record = &mut cursor.record;
    record.history_open_segment_first_seq = first_seq;
    record.history_open_segment_first_event_id = record.next_event_id.max(1);
    record.history_open_segment_bytes = 0;
    record.history_open_segment_superseded_value_count = 0;
}

/// Returns the superseded seq and value ID of a value garbage key.
fn garbage_identity(key: &[u8], prefix: &[u8]) -> Result<(u64, ValueId), ApplyError> {
    let suffix = key
        .strip_prefix(prefix)
        .filter(|suffix| suffix.len() == 20)
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "value garbage key must have a 20-byte suffix".to_owned(),
        })?;
    let (seq, value_id) = suffix.split_at(8);
    let seq = u64::from_be_bytes(
        seq.try_into()
            .expect("the suffix starts with eight seq bytes"),
    );
    let value_id =
        ValueId::from_key_suffix(value_id).expect("the suffix ends with a twelve-byte value ID");
    Ok((seq, value_id))
}
