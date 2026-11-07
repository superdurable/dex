// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Deleting local history that uploaded segments hold (plan section 5.5).
//!
//! Only rows and values of seqs that an UPLOADED segment covers are deleted. Seqs that belong
//! to no segment, such as commands applied while offload was off and the unsealed rest of an
//! abandoned migration, keep their local rows, which follow window pruning. Each command deletes
//! a bounded amount and records how far it got, so later commands of the run continue.

use dexzd_keyspace::row_keys::{
    checkpoint_key, history_record_key, history_row_metadata_key, history_segment_value_key,
    value_row_key,
};
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId, run_table_prefix};
use dexzd_protocol::dexzd::v1::{
    HistoryRowMetadata, HistorySegmentRecord, HistorySegmentValueRecord, ValueBirthRecord,
    ValueGarbageRecord,
};
use prost::Message;

use crate::ApplyError;
use crate::command_handlers::run_cursor::RunCursor;
use crate::history::{genesis_seq, metadata_identity};
use crate::history_offload::offload_mode::is_history_offload_enabled;
use crate::history_offload::segment_catalog::{CatalogPosition, SegmentCatalog, is_uploaded};
use crate::history_offload::segment_seal::garbage_identity;
use crate::state_access::{FlowTransaction, RunRows};

/// How long a superseded value stays local after the command that superseded it, by command
/// time, so a reference a Worker or Client just read still resolves locally (plan section 5.11).
pub(crate) const SUPERSEDED_VALUE_GRACE_MICROS: i64 = 10 * 60 * 1_000_000;
/// History metadata rows and catalog lookups one command examines to delete uploaded rows.
const MAX_HISTORY_ROWS_PER_COMMAND: usize = 4096;
/// Superseded values and catalog lookups one command examines to offload uploaded values.
const MAX_SUPERSEDED_VALUES_PER_COMMAND: usize = 4096;

/// Deletes, within per-command limits, the local history records and checkpoints of uploaded
/// segments and the superseded values whose grace period ended, while the partition offloads
/// history.
///
/// Genesis keeps its record and checkpoint. Metadata stays for the window, except for records
/// that are neither semantic nor hold events. An offloaded value keeps its birth record, marked
/// offloaded, so LoadBlobs finds its segment. `now_micros` is the command's proposal time.
pub(crate) fn delete_uploaded_history(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<(), ApplyError> {
    if cursor.record.history_uploaded_through_seq == 0 || !is_history_offload_enabled(transaction)?
    {
        return Ok(());
    }
    let genesis = genesis_seq(transaction, cursor.rows)?;
    delete_uploaded_rows(transaction, cursor, genesis)?;
    offload_uploaded_values(transaction, cursor, now_micros)
}

/// Stamps every value this command superseded with the command's proposal time, from which
/// its grace period runs.
pub(crate) fn stamp_superseded_values(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    now_micros: i64,
) -> Result<(), ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::ValueGarbage);
    let mut stamped = Vec::new();
    for change in transaction.buffered_row_changes_with_prefix(ColumnFamily::Flow, &prefix)? {
        let (None, Some(current)) = (&change.previous, &change.current) else {
            continue;
        };
        let garbage = ValueGarbageRecord::decode(current.as_slice()).map_err(|error| {
            ApplyError::InvariantViolation {
                detail: format!("buffered value garbage row is corrupt: {error}"),
            }
        })?;
        if garbage.superseded_at_micros == 0 {
            stamped.push((
                change.key,
                ValueGarbageRecord {
                    superseded_at_micros: now_micros,
                    ..garbage
                },
            ));
        }
    }
    for (key, garbage) in stamped {
        transaction.put_message(ColumnFamily::Flow, key, &garbage);
    }
    Ok(())
}

/// Where a seq at most the uploaded seq U falls for local deletion.
enum UploadedRange {
    /// An UPLOADED segment covers the seq.
    Uploaded(HistorySegmentRecord),
    /// No UPLOADED segment covers the seqs from the looked-up one to before `next_seq`.
    NotUploaded { next_seq: u64 },
}

fn uploaded_range_at(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    uploaded_through_seq: u64,
    seq: u64,
) -> Result<UploadedRange, ApplyError> {
    let catalog = SegmentCatalog::new(transaction, rows, uploaded_through_seq);
    Ok(match catalog.position(seq)? {
        CatalogPosition::Covered(segment) if is_uploaded(&segment) => {
            UploadedRange::Uploaded(segment)
        }
        CatalogPosition::Covered(segment) => UploadedRange::NotUploaded {
            next_seq: segment.last_seq.saturating_add(1),
        },
        CatalogPosition::Gap { next_first_seq } => UploadedRange::NotUploaded {
            next_seq: next_first_seq.unwrap_or(uploaded_through_seq.saturating_add(1)),
        },
    })
}

/// Deletes the history records and checkpoints of uploaded segments after the run's deletion
/// seq, moving that seq forward.
fn delete_uploaded_rows(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    genesis: u64,
) -> Result<(), ApplyError> {
    let rows = cursor.rows;
    let uploaded_through_seq = cursor.record.history_uploaded_through_seq;
    let mut budget = MAX_HISTORY_ROWS_PER_COMMAND;
    let mut seq = cursor
        .record
        .history_local_rows_deleted_through_seq
        .max(genesis)
        .saturating_add(1);
    while seq <= uploaded_through_seq && budget > 0 {
        budget -= 1;
        let (done_through, is_stopped) =
            match uploaded_range_at(transaction, rows, uploaded_through_seq, seq)? {
                UploadedRange::Uploaded(segment) => {
                    match delete_rows_of_seqs(
                        transaction,
                        rows,
                        seq,
                        segment.last_seq,
                        &mut budget,
                    )? {
                        RangeProgress::Complete => (segment.last_seq, false),
                        RangeProgress::StoppedAt(stop_seq) => (stop_seq - 1, true),
                    }
                }
                UploadedRange::NotUploaded { next_seq } => (next_seq - 1, false),
            };
        cursor.record.history_local_rows_deleted_through_seq = cursor
            .record
            .history_local_rows_deleted_through_seq
            .max(done_through);
        if is_stopped {
            break;
        }
        seq = done_through.saturating_add(1);
    }
    Ok(())
}

/// How far a bounded pass over a seq range got.
enum RangeProgress {
    Complete,
    /// The pass stopped before finishing this seq; a later command resumes there.
    StoppedAt(u64),
}

/// Deletes the history rows of `[first_seq, last_seq]` within `budget` examined metadata rows.
fn delete_rows_of_seqs(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    first_seq: u64,
    last_seq: u64,
    budget: &mut usize,
) -> Result<RangeProgress, ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRowMetadata);
    let start = history_row_metadata_key(
        rows.flow_hash,
        rows.run_id,
        first_seq,
        RunTable::HistoryRecord,
        0,
    );
    let mut end = prefix.clone();
    end.extend_from_slice(&last_seq.saturating_add(1).to_be_bytes());
    let limit = *budget;
    let page = transaction.scan_messages::<HistoryRowMetadata>(
        ColumnFamily::Flow,
        &start,
        &end,
        limit,
        "history metadata",
    )?;
    *budget -= page.len();
    let is_complete = page.len() < limit;
    let mut last_scanned_seq = first_seq;
    for (metadata_key, metadata) in page {
        let (seq, history_table, attempt) = metadata_identity(&metadata_key, &prefix)?;
        last_scanned_seq = seq;
        // A zero size marks a row whose body an earlier command already deleted.
        if metadata.row_size_bytes == 0 {
            continue;
        }
        let row_key = match history_table {
            RunTable::HistoryRecord => history_record_key(rows.flow_hash, rows.run_id, seq),
            RunTable::Checkpoint => checkpoint_key(rows.flow_hash, rows.run_id, attempt, seq),
            _ => {
                return Err(ApplyError::InvariantViolation {
                    detail: "history metadata names an unexpected table".to_owned(),
                });
            }
        };
        if history_table == RunTable::HistoryRecord && !is_metadata_kept_after_upload(&metadata) {
            transaction.delete(ColumnFamily::History, row_key);
            continue;
        }
        transaction.delete_history_row_keeping_metadata(row_key);
        transaction.put_message(
            ColumnFamily::Flow,
            metadata_key,
            &HistoryRowMetadata {
                row_size_bytes: 0,
                ..metadata
            },
        );
    }
    Ok(if is_complete {
        RangeProgress::Complete
    } else {
        RangeProgress::StoppedAt(last_scanned_seq)
    })
}

/// Whether a record's metadata stays after its row is deleted, until the window passes it.
///
/// Only records known to be neither semantic nor hold events, pending method calls, or a close
/// lose it: no reader needs them, and long heartbeat-only Steps would otherwise grow metadata.
/// Rows written before these fields existed keep their metadata.
fn is_metadata_kept_after_upload(metadata: &HistoryRowMetadata) -> bool {
    let has_complete_fields =
        metadata.window_event_count_before.is_some() && metadata.last_assigned_event_id > 0;
    !has_complete_fields
        || metadata.is_semantic
        || metadata.is_external_input
        || metadata.max_event_id != 0
        || !metadata.pending_step_methods.is_empty()
        || metadata.flow_closed_message.is_some()
}

/// Offloads the superseded values of uploaded segments after the run's offload seq, moving that
/// seq forward; it stops at the first value still in its grace period.
fn offload_uploaded_values(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<(), ApplyError> {
    let rows = cursor.rows;
    let uploaded_through_seq = cursor.record.history_uploaded_through_seq;
    let mut budget = MAX_SUPERSEDED_VALUES_PER_COMMAND;
    let mut seq = cursor
        .record
        .history_values_offloaded_through_seq
        .saturating_add(1);
    while seq <= uploaded_through_seq && budget > 0 {
        budget -= 1;
        let (done_through, is_stopped) =
            match uploaded_range_at(transaction, rows, uploaded_through_seq, seq)? {
                UploadedRange::Uploaded(segment) => {
                    match offload_values_of_segment(
                        transaction,
                        rows,
                        &segment,
                        seq,
                        now_micros,
                        &mut budget,
                    )? {
                        RangeProgress::Complete => (segment.last_seq, false),
                        RangeProgress::StoppedAt(stop_seq) => (stop_seq - 1, true),
                    }
                }
                UploadedRange::NotUploaded { next_seq } => (next_seq - 1, false),
            };
        cursor.record.history_values_offloaded_through_seq = cursor
            .record
            .history_values_offloaded_through_seq
            .max(done_through);
        if is_stopped {
            break;
        }
        seq = done_through.saturating_add(1);
    }
    Ok(())
}

/// Offloads the values superseded in `[first_seq, segment.last_seq]` that `segment` lists,
/// within `budget` examined values. Values it does not list stay local until the window passes
/// them.
fn offload_values_of_segment(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    segment: &HistorySegmentRecord,
    first_seq: u64,
    now_micros: i64,
    budget: &mut usize,
) -> Result<RangeProgress, ApplyError> {
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::ValueGarbage);
    let mut start = prefix.clone();
    start.extend_from_slice(&first_seq.to_be_bytes());
    let mut end = prefix.clone();
    end.extend_from_slice(&segment.last_seq.saturating_add(1).to_be_bytes());
    let limit = *budget;
    let page = transaction.scan_messages::<ValueGarbageRecord>(
        ColumnFamily::Flow,
        &start,
        &end,
        limit,
        "value garbage",
    )?;
    *budget -= page.len();
    let is_complete = page.len() < limit;
    let mut last_scanned_seq = first_seq;
    for (garbage_key, garbage) in page {
        let (superseded_seq, value_id) = garbage_identity(&garbage_key, &prefix)?;
        last_scanned_seq = superseded_seq;
        if !is_listed_in_segment(transaction, rows, segment, value_id)? {
            continue;
        }
        let is_in_grace = garbage.superseded_at_micros != 0
            && now_micros.saturating_sub(garbage.superseded_at_micros)
                < SUPERSEDED_VALUE_GRACE_MICROS;
        if is_in_grace {
            return Ok(RangeProgress::StoppedAt(superseded_seq));
        }
        let birth_key = value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id);
        let birth = transaction
            .get_message::<ValueBirthRecord>(ColumnFamily::Flow, &birth_key, "value birth")?
            .filter(|birth| birth.garbage_seq == superseded_seq && !birth.is_offloaded)
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: format!(
                    "value {}/{} superseded at seq {superseded_seq} has no matching birth record",
                    value_id.run_seq, value_id.index_in_command
                ),
            })?;
        transaction.delete(
            ColumnFamily::Value,
            value_row_key(rows.flow_hash, rows.run_id, RunTable::Value, value_id),
        );
        transaction.delete(ColumnFamily::Flow, garbage_key);
        transaction.put_message(
            ColumnFamily::Flow,
            birth_key,
            &ValueBirthRecord {
                is_offloaded: true,
                ..birth
            },
        );
    }
    Ok(if is_complete {
        RangeProgress::Complete
    } else {
        RangeProgress::StoppedAt(last_scanned_seq)
    })
}

/// Whether the segment's value list names `value_id`, so its object holds the value.
fn is_listed_in_segment(
    transaction: &FlowTransaction<'_>,
    rows: RunRows,
    segment: &HistorySegmentRecord,
    value_id: ValueId,
) -> Result<bool, ApplyError> {
    Ok(transaction
        .get_message::<HistorySegmentValueRecord>(
            ColumnFamily::Flow,
            &history_segment_value_key(rows.flow_hash, rows.run_id, segment.first_seq, value_id),
            "history segment value",
        )?
        .is_some())
}
