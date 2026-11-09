// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Migrating history a run recorded before it had segment state (plan section 5.17).

use std::collections::BTreeMap;

use dexzd_keyspace::row_keys::history_record_key;
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::{HistoryRecord, HistoryRowMetadata, RunRecord, ValueGarbageRecord};

use crate::ApplyError;
use crate::command_handlers::run_cursor::RunCursor;
use crate::history::genesis_seq;
use crate::history_offload::offload_mode::is_run_history_offloaded;
use crate::history_offload::segment_seal::{
    MAX_SEGMENT_SUPERSEDED_VALUES, SealedSegmentBounds, garbage_identity,
    open_segment_after_command, seal_history_segment_if_due, segment_target_bytes,
    write_sealed_segment,
};
use crate::state_access::FlowTransaction;

/// Bounds the history rows of one migration segment, so cutting it reads a bounded page.
const MAX_MIGRATION_SEGMENT_HISTORY_ROWS: usize = 10_000;
/// FlowStart records event 1 of every run.
const FIRST_EVENT_ID: i64 = 1;

/// Seals a due open segment, or starts or continues migrating older history, sealing at most one.
///
/// `has_closed_run` tells whether this command closed the run; `now_micros` is its proposal time.
pub(crate) fn seal_or_migrate_history_segment(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
    has_closed_run: bool,
) -> Result<(), ApplyError> {
    let record = &cursor.record;
    if record.history_open_segment_first_seq == 0 {
        // Closed runs never start: migrating them only uploads history that purge deletes next.
        if is_running(record)
            && !record.is_purging
            && is_run_history_offloaded(transaction, record)?
        {
            start_migration(transaction, cursor)?;
            migrate_one_segment(transaction, cursor, now_micros)?;
        }
        return Ok(());
    }
    if record.history_migration_started_seq == 0 {
        return seal_history_segment_if_due(transaction, cursor, now_micros, has_closed_run);
    }
    if !is_run_history_offloaded(transaction, record)? {
        // The unsealed rest joins the gap of commands applied while offload is off.
        cursor.record.history_migration_started_seq = 0;
        return seal_history_segment_if_due(transaction, cursor, now_micros, has_closed_run);
    }
    if record.is_purging || !(has_closed_run || is_running(record)) {
        return Ok(());
    }
    migrate_one_segment(transaction, cursor, now_micros)
}

/// Opens the segment state of a run without one at its first seq and event 1.
fn start_migration(
    transaction: &FlowTransaction<'_>,
    cursor: &mut RunCursor,
) -> Result<(), ApplyError> {
    let first_seq = genesis_seq(transaction, cursor.rows)?;
    let record = &mut cursor.record;
    record.history_migration_started_seq = cursor.seq;
    record.history_open_segment_first_seq = first_seq;
    record.history_open_segment_first_event_id = FIRST_EVENT_ID;
    record.history_open_segment_bytes = 0;
    record.history_open_segment_superseded_value_count = 0;
    Ok(())
}

/// Seals the next migration segment; the migration ends once a segment reaches this command.
fn migrate_one_segment(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<(), ApplyError> {
    let bounds = cut_migration_segment(transaction, cursor)?;
    write_sealed_segment(transaction, cursor, &bounds, now_micros)?;
    if bounds.last_seq == cursor.seq {
        cursor.record.history_migration_started_seq = 0;
        open_segment_after_command(cursor);
    } else {
        cursor.record.history_open_segment_first_seq = bounds.last_seq + 1;
        cursor.record.history_open_segment_first_event_id = bounds.last_event_id + 1;
    }
    Ok(())
}

/// Stored history of one seq inside the migration range.
#[derive(Default)]
struct SeqHistory {
    bytes: u64,
    history_rows: usize,
    superseded_values: u64,
    record: Option<HistoryRowMetadata>,
}

/// What a migration segment holds so far.
#[derive(Default)]
struct SegmentTotals {
    bytes: u64,
    history_rows: usize,
    superseded_values: u64,
    records: usize,
}

impl SegmentTotals {
    fn add(&mut self, seq_history: &SeqHistory) {
        self.bytes = self.bytes.saturating_add(seq_history.bytes);
        self.history_rows += seq_history.history_rows;
        self.superseded_values += seq_history.superseded_values;
        self.records += usize::from(seq_history.record.is_some());
    }

    /// Two records at least, so each command seals more history than it adds.
    fn is_complete(&self, target_bytes: u64) -> bool {
        self.records >= 2
            && (self.bytes >= target_bytes
                || self.superseded_values >= MAX_SEGMENT_SUPERSEDED_VALUES
                || self.history_rows >= MAX_MIGRATION_SEGMENT_HISTORY_ROWS)
    }
}

/// Returns the bounds of the next migration segment, which starts at the open segment and ends
/// at a whole seq: the first one where the segment holds two history records and reaches the
/// target bytes, the superseded value limit, or the history row limit; otherwise this command's
/// seq. When a bounded page ends first, the segment ends with that page or its second record.
fn cut_migration_segment(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
) -> Result<SealedSegmentBounds, ApplyError> {
    let first_seq = cursor.record.history_open_segment_first_seq;
    let first_event_id = cursor.record.history_open_segment_first_event_id;
    let current_seq = cursor.seq;
    let mut seqs = BTreeMap::<u64, SeqHistory>::new();
    let metadata_complete_through = scan_history_metadata(transaction, cursor, &mut seqs)?;
    let garbage_complete_through = scan_superseded_values(transaction, cursor, &mut seqs)?;
    let complete_through = metadata_complete_through.min(garbage_complete_through);
    // The command's own record is stored, so the range holds at least one record.
    let second_record_seq = seqs
        .iter()
        .filter(|(_, seq_history)| seq_history.record.is_some())
        .map(|(seq, _)| *seq)
        .nth(1)
        .unwrap_or(current_seq);
    let target_bytes = segment_target_bytes(&cursor.record);
    let mut totals = SegmentTotals::default();
    let cut_seq = seqs
        .iter()
        .take_while(|(seq, _)| **seq <= complete_through)
        .find_map(|(seq, seq_history)| {
            totals.add(seq_history);
            totals.is_complete(target_bytes).then_some(*seq)
        });
    // Whole seqs can pass the limits; the command that recorded such a seq wrote as many rows.
    let last_seq = cut_seq.unwrap_or_else(|| complete_through.max(second_record_seq));
    let last_event_id = if last_seq == current_seq {
        cursor.record.next_event_id.max(1) - 1
    } else {
        newest_event_id_through(transaction, cursor, &seqs, last_seq, first_event_id - 1)?
    };
    Ok(SealedSegmentBounds {
        first_seq,
        last_seq,
        first_event_id,
        last_event_id,
    })
}

/// Adds the history records and checkpoints of the migration range to `seqs`, reading at most
/// one row more than the row limit. Returns the last seq whose rows were all read.
fn scan_history_metadata(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    seqs: &mut BTreeMap<u64, SeqHistory>,
) -> Result<u64, ApplyError> {
    let rows = cursor.rows;
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRowMetadata);
    let limit = MAX_MIGRATION_SEGMENT_HISTORY_ROWS + 1;
    let page = transaction.scan_messages::<HistoryRowMetadata>(
        ColumnFamily::Flow,
        &seq_bound(&prefix, cursor.record.history_open_segment_first_seq),
        &seq_bound(&prefix, cursor.seq + 1),
        limit,
        "history metadata",
    )?;
    let is_truncated = page.len() == limit;
    let mut last_seq = cursor.seq;
    for (key, metadata) in page {
        let suffix = key
            .strip_prefix(prefix.as_slice())
            .filter(|suffix| suffix.len() == 13)
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: "history metadata key must have a 13-byte suffix".to_owned(),
            })?;
        let seq = u64::from_be_bytes(suffix[..8].try_into().expect("eight seq bytes"));
        last_seq = seq;
        let seq_history = seqs.entry(seq).or_default();
        seq_history.bytes = seq_history.bytes.saturating_add(metadata.row_size_bytes);
        seq_history.history_rows += 1;
        if suffix[8] == RunTable::HistoryRecord as u8 {
            seq_history.record = Some(metadata);
        }
    }
    Ok(if is_truncated {
        last_seq - 1
    } else {
        cursor.seq
    })
}

/// Adds the values superseded inside the migration range to `seqs`, reading at most one more
/// than the superseded value limit. Returns the last seq whose values were all read.
fn scan_superseded_values(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    seqs: &mut BTreeMap<u64, SeqHistory>,
) -> Result<u64, ApplyError> {
    let rows = cursor.rows;
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::ValueGarbage);
    let limit =
        usize::try_from(MAX_SEGMENT_SUPERSEDED_VALUES).expect("the value limit fits in usize") + 1;
    let page = transaction.scan_messages::<ValueGarbageRecord>(
        ColumnFamily::Flow,
        &seq_bound(&prefix, cursor.record.history_open_segment_first_seq),
        &seq_bound(&prefix, cursor.seq + 1),
        limit,
        "value garbage",
    )?;
    let is_truncated = page.len() == limit;
    let mut last_seq = cursor.seq;
    for (key, garbage) in page {
        let (seq, _) = garbage_identity(&key, &prefix)?;
        last_seq = seq;
        let seq_history = seqs.entry(seq).or_default();
        seq_history.bytes = seq_history.bytes.saturating_add(garbage.size_bytes);
        seq_history.superseded_values += 1;
    }
    Ok(if is_truncated {
        last_seq - 1
    } else {
        cursor.seq
    })
}

/// Returns the newest event ID that the records in `seqs` through `last_seq` assigned, or
/// `before_range` when none did.
///
/// Records older than `last_assigned_event_id` are decoded; events of pruned records then widen
/// the next range.
fn newest_event_id_through(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    seqs: &BTreeMap<u64, SeqHistory>,
    last_seq: u64,
    before_range: i64,
) -> Result<i64, ApplyError> {
    let mut newest = before_range;
    for (seq, seq_history) in seqs.range(..=last_seq) {
        let Some(metadata) = &seq_history.record else {
            continue;
        };
        if metadata.last_assigned_event_id > 0 {
            newest = newest.max(metadata.last_assigned_event_id);
            continue;
        }
        let key = history_record_key(cursor.rows.flow_hash, cursor.rows.run_id, *seq);
        let record = transaction
            .get_message::<HistoryRecord>(ColumnFamily::History, &key, "history record")?
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: format!("history record {seq} has metadata but no row"),
            })?;
        if let Some(event_id) = record.events.iter().map(|event| event.event_id).max() {
            newest = newest.max(event_id);
        }
    }
    Ok(newest)
}

/// Returns the first key of `seq` in a run table whose keys start with an eight-byte seq.
fn seq_bound(prefix: &[u8], seq: u64) -> Vec<u8> {
    let mut key = prefix.to_vec();
    key.extend_from_slice(&seq.to_be_bytes());
    key
}

fn is_running(record: &RunRecord) -> bool {
    record.status == FlowStatus::Running as i32
}
