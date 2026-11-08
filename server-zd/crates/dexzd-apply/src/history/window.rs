// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The time travel window (plan section 8.4): about the newest N history events, pruned about M at
//! a time.
//!
//! FlowStart and the genesis checkpoint are kept until the run is deleted (D12).

use std::collections::{BTreeMap, BTreeSet};

use dexzd_keyspace::row_keys::{
    checkpoint_key, history_record_key, history_row_metadata_key, prefix_range,
};
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::{HistoryRowMetadata, RunRecord};

use crate::ApplyError;
use crate::command_handlers::run_cursor::RunCursor;
use crate::history::lineage::{genesis_seq, required_attempt_record};
use crate::history_offload::offload_mode::is_history_offload_enabled;
use crate::history_offload::segment_upload::first_seq_kept_until_upload;
use crate::state_access::FlowTransaction;

const HISTORY_ROWS_PER_COMMAND: usize = 128;

/// Metadata pages one command may scan to prune an event chunk; covers the default 1000-event chunk.
const EVENT_CHUNK_PAGES_PER_COMMAND: usize = 16;

pub(crate) enum WindowAdvanceTarget {
    /// Window events to prune; W stops at the first semantic point that prunes at least this many.
    Events(u64),
    GarbageSeq(u64),
}

/// A run's window size N and prune chunk M, both in window events.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct HistoryWindowSize {
    window_events: u64,
    prune_chunk_events: u64,
}

impl HistoryWindowSize {
    /// Returns the size from the run's FlowConfig override or its settings; `None` never prunes.
    ///
    /// Settings of zero never prune. A small window prunes at most a tenth of itself at once.
    fn of_run(record: &RunRecord) -> Result<Option<Self>, ApplyError> {
        let settings = record.settings.unwrap_or_default();
        let window_events = match record
            .flow_config
            .as_ref()
            .and_then(|flow_config| flow_config.history_window_events)
        {
            Some(configured) => u64::try_from(configured)
                .ok()
                .filter(|window_events| *window_events > 0)
                .ok_or_else(|| ApplyError::InvariantViolation {
                    detail: format!(
                        "run FlowConfig history_window_events {configured} passed validation \
                         but is not positive"
                    ),
                })?,
            None => u64::from(settings.history_window_events),
        };
        if window_events == 0 {
            return Ok(None);
        }
        let prune_chunk_events = u64::from(settings.history_prune_chunk_events)
            .min(window_events / 10)
            .max(1);
        Ok(Some(Self {
            window_events,
            prune_chunk_events,
        }))
    }
}

/// Advances the window start W when the window holds more than N + M window events.
///
/// It prunes M, or back to N + M when a shrunk window holds more, within a bounded scan.
pub(crate) fn advance_window_if_full(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
) -> Result<(), ApplyError> {
    let Some(size) = HistoryWindowSize::of_run(&cursor.record)? else {
        return Ok(());
    };
    let window_event_count =
        cursor
            .record
            .window_event_count
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: "window pruning runs before the command counts window events".to_owned(),
            })?;
    let in_window = window_event_count.saturating_sub(cursor.record.pruned_window_event_count);
    let full_window_events = size.window_events + size.prune_chunk_events;
    if in_window <= full_window_events {
        return Ok(());
    }
    let settings = cursor.record.settings.unwrap_or_default();
    // Offloaded superseded values leave local disk, so the cap applies only while offload is off.
    if !is_history_offload_enabled(transaction)?
        && crate::values::projected_superseded_value_bytes(transaction, cursor)?
            > settings.max_retained_superseded_bytes
    {
        return Ok(());
    }
    advance_window(
        transaction,
        cursor,
        WindowAdvanceTarget::Events(size.prune_chunk_events.max(in_window - full_window_events)),
    )?;
    Ok(())
}

/// Moves W past up to the requested window events, keeping what rebuilds every later point.
///
/// Returns false without a later semantic point; the superseded value byte cap also calls this.
pub(crate) fn advance_window(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    target: WindowAdvanceTarget,
) -> Result<bool, ApplyError> {
    if cursor.has_used_history_window_page
        || matches!(target, WindowAdvanceTarget::Events(0))
        || matches!(target, WindowAdvanceTarget::GarbageSeq(seq) if seq < cursor.record.window_start_seq)
    {
        return Ok(false);
    }
    let Some(pruned) = find_new_window_start(transaction, cursor, target)? else {
        return Ok(false);
    };
    let record = &mut cursor.record;
    record.max_pruned_event_id = pruned.last_event_id_before_window_start;
    record.window_start_seq = pruned.window_start_seq;
    record.window_scan_seq = 0;
    record.window_scan_last_event_id = 0;
    record.pruned_semantic_event_count += pruned.semantic_points;
    record.pruned_window_event_count = record
        .pruned_window_event_count
        .max(pruned.window_event_count_before);
    record.max_pruned_external_input_seq = record
        .max_pruned_external_input_seq
        .max(pruned.max_external_input_seq);
    record.last_pruned_semantic_timestamp_micros = record
        .last_pruned_semantic_timestamp_micros
        .max(pruned.max_semantic_timestamp_micros);
    crate::values::prune_garbage(transaction, cursor, pruned.window_start_seq)?;
    Ok(true)
}

struct PrunedChunk {
    window_start_seq: u64,
    semantic_points: u64,
    /// Window events recorded before the new W, which all leave the window.
    window_event_count_before: u64,
    max_external_input_seq: u64,
    max_semantic_timestamp_micros: i64,
    /// Newest event ID of the record before the new W; zero when the scan did not see it.
    last_event_id_before_window_start: i64,
}

/// Skips up to one page of history metadata; the next semantic record becomes the new W.
///
/// An event target may scan up to 16 pages, so one command prunes a whole default chunk.
fn find_new_window_start(
    transaction: &FlowTransaction<'_>,
    cursor: &mut RunCursor,
    target: WindowAdvanceTarget,
) -> Result<Option<PrunedChunk>, ApplyError> {
    let rows = cursor.rows;
    let current_start = cursor.record.window_start_seq;
    let pruned_window_event_count = cursor.record.pruned_window_event_count;
    let start_seq = cursor.record.window_scan_seq.max(current_start);
    let start = history_row_metadata_key(
        rows.flow_hash,
        rows.run_id,
        start_seq,
        RunTable::HistoryRecord,
        0,
    );
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRowMetadata);
    let (_, end) = prefix_range(&prefix);
    cursor.has_used_history_window_page = true;
    let start_metadata_key = history_row_metadata_key(
        rows.flow_hash,
        rows.run_id,
        current_start,
        RunTable::HistoryRecord,
        0,
    );
    let start_metadata: HistoryRowMetadata = transaction
        .get_message(ColumnFamily::Flow, &start_metadata_key, "history metadata")?
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!("window start {current_start} has no history metadata"),
        })?;
    let mut previous_semantic_seq = current_start;
    let mut previous_semantic_timestamp_micros = start_metadata.timestamp_micros;
    let mut was_previous_external = start_metadata.is_external_input;
    let mut max_external_input_seq = 0;
    let mut max_semantic_timestamp_micros = i64::MIN;
    let mut semantic_points = 0;
    let mut candidate = None;
    let mut last_scanned_seq = None;
    // A resumed scan starts after the rows an earlier command inspected; the W row seeds a new one.
    let mut last_record_event_id = if start_seq > current_start {
        cursor.record.window_scan_last_event_id
    } else {
        0
    };
    let page_count = match target {
        WindowAdvanceTarget::Events(_) => EVENT_CHUNK_PAGES_PER_COMMAND,
        WindowAdvanceTarget::GarbageSeq(_) => 1,
    };
    let mut page_start = start;
    'pages: for _ in 0..page_count {
        let page = transaction.scan_messages::<HistoryRowMetadata>(
            ColumnFamily::Flow,
            &page_start,
            &end,
            HISTORY_ROWS_PER_COMMAND,
            "history metadata",
        )?;
        let is_last_page = page.len() < HISTORY_ROWS_PER_COMMAND;
        if let Some((last_key, _)) = page.last() {
            // The smallest key after the last one scanned starts the next page.
            page_start.clone_from(last_key);
            page_start.push(0);
        }
        for (key, metadata) in page {
            let (seq, history_table, _) = metadata_identity(&key, &prefix)?;
            last_scanned_seq = Some(seq);
            if history_table != RunTable::HistoryRecord {
                continue;
            }
            // Offload deletes only rows without events, so the newest row seen ends the events.
            let last_event_id_before_row =
                std::mem::replace(&mut last_record_event_id, metadata.last_assigned_event_id);
            if !metadata.is_semantic || seq <= current_start || seq > cursor.seq {
                continue;
            }
            if was_previous_external {
                max_external_input_seq = max_external_input_seq.max(previous_semantic_seq);
            }
            max_semantic_timestamp_micros =
                max_semantic_timestamp_micros.max(previous_semantic_timestamp_micros);
            semantic_points += 1;
            // Rows written before the window counted events count one per semantic point.
            let window_event_count_before = metadata
                .window_event_count_before
                .unwrap_or(pruned_window_event_count + semantic_points);
            candidate = Some(PrunedChunk {
                window_start_seq: seq,
                semantic_points,
                window_event_count_before,
                max_external_input_seq,
                max_semantic_timestamp_micros,
                last_event_id_before_window_start: last_event_id_before_row,
            });
            previous_semantic_seq = seq;
            previous_semantic_timestamp_micros = metadata.timestamp_micros;
            was_previous_external = metadata.is_external_input;
            match target {
                WindowAdvanceTarget::Events(limit)
                    if window_event_count_before.saturating_sub(pruned_window_event_count)
                        >= limit =>
                {
                    break 'pages;
                }
                WindowAdvanceTarget::GarbageSeq(garbage_seq) if seq > garbage_seq => break 'pages,
                _ => {}
            }
        }
        if is_last_page {
            break;
        }
    }
    if candidate.is_none()
        && let Some(last_seq) = last_scanned_seq
    {
        cursor.record.window_scan_seq = last_seq.saturating_add(1);
        cursor.record.window_scan_last_event_id = last_record_event_id;
    }
    Ok(candidate)
}

/// Deletes history records and checkpoints older than the checkpoint that rebuilds W.
///
/// Their metadata rows go too. A row that offload already deleted leaves only its metadata row
/// to delete. Moves the run's `history_window_pruned_through_seq` past what is deleted.
///
/// Rows after the run's uploaded seq stay while the partition offloads history or the run has a
/// SEALED segment.
pub(crate) fn prune_window_history(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
) -> Result<usize, ApplyError> {
    let rows = cursor.rows;
    let window_start_seq = cursor.record.window_start_seq;
    let window_metadata_key = history_row_metadata_key(
        rows.flow_hash,
        rows.run_id,
        window_start_seq,
        RunTable::HistoryRecord,
        0,
    );
    let window_metadata: HistoryRowMetadata = transaction
        .get_message(ColumnFamily::Flow, &window_metadata_key, "history metadata")?
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!("window start {window_start_seq} has no history metadata"),
        })?;
    let keep_from = required_attempt_record(transaction, rows, window_metadata.attempt)?
        .checkpoint_seqs
        .iter()
        .copied()
        .filter(|seq| *seq <= window_start_seq)
        .max()
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: format!(
                "run attempt {} has no checkpoint at or before seq {window_start_seq}",
                window_metadata.attempt
            ),
        })?;
    let keep_from = keep_from.min(first_seq_kept_until_upload(transaction, cursor)?);
    let genesis = genesis_seq(transaction, rows)?;
    if keep_from <= genesis + 1 {
        return Ok(0);
    }
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::HistoryRowMetadata);
    let start = history_row_metadata_key(
        rows.flow_hash,
        rows.run_id,
        genesis + 1,
        RunTable::HistoryRecord,
        0,
    );
    let end = history_row_metadata_key(
        rows.flow_hash,
        rows.run_id,
        keep_from,
        RunTable::HistoryRecord,
        0,
    );
    let page = transaction.scan_messages::<HistoryRowMetadata>(
        ColumnFamily::Flow,
        &start,
        &end,
        HISTORY_ROWS_PER_COMMAND,
        "history metadata",
    )?;
    let mut deleted_checkpoints = BTreeMap::<u32, BTreeSet<u64>>::new();
    let count = page.len();
    // Record rows sort before checkpoint rows, so every record through the last row is deleted.
    let pruned_through_seq = match page.last() {
        Some((key, _)) if count == HISTORY_ROWS_PER_COMMAND => metadata_identity(key, &prefix)?.0,
        _ => keep_from - 1,
    };
    cursor.record.history_window_pruned_through_seq = cursor
        .record
        .history_window_pruned_through_seq
        .max(pruned_through_seq);
    let mut identities = Vec::with_capacity(count);
    for (key, metadata) in &page {
        let (seq, history_table, attempt) = metadata_identity(key, &prefix)?;
        // Offload deleted the row of uploaded history and zeroed its size; only metadata remains.
        identities.push((
            key,
            seq,
            history_table,
            attempt,
            metadata.row_size_bytes > 0,
        ));
    }
    for (metadata_key, seq, history_table, attempt, has_row) in identities {
        transaction.delete(ColumnFamily::Flow, metadata_key.clone());
        match history_table {
            RunTable::HistoryRecord => {
                if has_row {
                    transaction.delete(
                        ColumnFamily::History,
                        history_record_key(rows.flow_hash, rows.run_id, seq),
                    );
                }
            }
            RunTable::Checkpoint => {
                if has_row {
                    transaction.delete(
                        ColumnFamily::History,
                        checkpoint_key(rows.flow_hash, rows.run_id, attempt, seq),
                    );
                }
                deleted_checkpoints.entry(attempt).or_default().insert(seq);
            }
            _ => {
                return Err(ApplyError::InvariantViolation {
                    detail: "history metadata names an unexpected table".to_owned(),
                });
            }
        }
    }
    for (attempt, deleted_seqs) in deleted_checkpoints {
        let mut attempt_record = required_attempt_record(transaction, rows, attempt)?;
        attempt_record
            .checkpoint_seqs
            .retain(|seq| !deleted_seqs.contains(seq));
        rows.put_attempt_record(transaction, &attempt_record);
    }
    Ok(count)
}

pub(crate) fn metadata_identity(
    key: &[u8],
    prefix: &[u8],
) -> Result<(u64, RunTable, u32), ApplyError> {
    let suffix = key
        .strip_prefix(prefix)
        .ok_or_else(|| ApplyError::InvariantViolation {
            detail: "history metadata key is outside its run".to_owned(),
        })?;
    if suffix.len() != 13 {
        return Err(ApplyError::InvariantViolation {
            detail: "history metadata key must have a 13-byte suffix".to_owned(),
        });
    }
    let seq = u64::from_be_bytes(suffix[..8].try_into().expect("eight-byte seq"));
    let history_table =
        RunTable::from_byte(suffix[8]).map_err(|error| ApplyError::InvariantViolation {
            detail: format!("history metadata table is invalid: {error}"),
        })?;
    let attempt = u32::from_be_bytes(suffix[9..].try_into().expect("four-byte attempt"));
    Ok((seq, history_table, attempt))
}
