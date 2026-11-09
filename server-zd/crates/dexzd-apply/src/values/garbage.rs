// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::row_keys::{prefix_range, value_row_key};
use dexzd_keyspace::{ColumnFamily, RunTable, run_table_prefix};
use dexzd_protocol::dexzd::v1::ValueGarbageRecord;

use crate::ApplyError;
use crate::command_handlers::run_cursor::RunCursor;
use crate::history::{
    HISTORY_ROWS_PER_COMMAND, LocalCleanupBacklog, WindowAdvanceTarget, advance_window,
    prune_window_history,
};
use crate::history_offload::offload_mode::is_superseded_byte_cap_applied;
use crate::history_offload::segment_expiry::expire_passed_history_segments;
use crate::history_offload::segment_seal::garbage_identity;
use crate::history_offload::segment_upload::first_seq_kept_until_upload;
use crate::state_access::FlowTransaction;
use crate::values::projected_superseded_value_bytes;

const GARBAGE_ROWS_PER_CALL: usize = 128;

/// Collects at most 128 values whose last holder disappeared before the window start, and only
/// within the run's uploaded seqs while the partition offloads or the run has a SEALED segment.
///
/// Scans from `value_garbage_pruned_before_seq`, skipping deleted rows the store has not
/// compacted.
pub(crate) fn prune_garbage(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    window_start_seq: u64,
) -> Result<usize, ApplyError> {
    let rows = cursor.rows;
    let prefix = run_table_prefix(rows.flow_hash, rows.run_id, RunTable::ValueGarbage);
    let delete_before_seq = window_start_seq.min(first_seq_kept_until_upload(transaction, cursor)?);
    let scan_start_seq = cursor.record.value_garbage_pruned_before_seq;
    debug_assert!(
        transaction
            .first_buffered_put_key(
                ColumnFamily::Flow,
                &prefix,
                &garbage_seq_key(&prefix, scan_start_seq)
            )
            .is_none(),
        "value garbage is added at or after the window start, which the garbage cursor never passes"
    );
    if scan_start_seq >= delete_before_seq {
        return Ok(0);
    }
    let garbage = transaction.scan_messages::<ValueGarbageRecord>(
        ColumnFamily::Flow,
        &garbage_seq_key(&prefix, scan_start_seq),
        &garbage_seq_key(&prefix, delete_before_seq),
        GARBAGE_ROWS_PER_CALL,
        "value garbage",
    )?;
    let mut collected = 0;
    let mut last_collected_seq = scan_start_seq;
    for (garbage_key, _) in garbage {
        let (lost_last_holder_seq, value_id) = garbage_identity(&garbage_key, &prefix)?;
        last_collected_seq = lost_last_holder_seq;
        transaction.delete(
            ColumnFamily::Value,
            value_row_key(rows.flow_hash, rows.run_id, RunTable::Value, value_id),
        );
        transaction.delete(
            ColumnFamily::Flow,
            value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id),
        );
        transaction.delete(ColumnFamily::Flow, garbage_key);
        collected += 1;
    }
    // A full page may end between rows of its last seq, so that seq is scanned again.
    let pruned_before_seq = if collected == GARBAGE_ROWS_PER_CALL {
        last_collected_seq
    } else {
        delete_before_seq
    };
    // Window starts begin at 1, so leaving 1 unrecorded keeps never-pruned runs unchanged.
    if pruned_before_seq > cursor.record.value_garbage_pruned_before_seq.max(1) {
        cursor.record.value_garbage_pruned_before_seq = pruned_before_seq;
    }
    Ok(collected)
}

/// Returns the superseded bytes beyond this run's configured window cap.
#[expect(dead_code, reason = "Available to window maintenance handlers.")]
pub(crate) fn superseded_bytes_over_cap(cursor: &RunCursor) -> u64 {
    let usage = cursor.record.storage.unwrap_or_default();
    let settings = cursor.record.settings.unwrap_or_default();
    usage
        .superseded_value_bytes
        .saturating_sub(settings.max_retained_superseded_bytes)
}

/// Advances the semantic window toward its byte cap while retaining the current point.
///
/// While the partition offloads history, superseded values leave local disk once uploaded, so
/// the cap moves nothing; garbage and window history are still pruned. Segments that pruning
/// passed then expire; `now_micros` is the command's proposal time. The caller reconciles the
/// run's storage.
///
/// Returns the backlog, unfinished when a per-command bound stopped pruning or expiry.
pub(crate) fn enforce_superseded_byte_cap(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<LocalCleanupBacklog, ApplyError> {
    let mut backlog = LocalCleanupBacklog::default();
    prune_garbage_before_window(transaction, cursor, &mut backlog)?;
    if is_superseded_byte_cap_applied(transaction, &cursor.record)? {
        let superseded_bytes = projected_superseded_value_bytes(transaction, cursor)?;
        let cap = cursor
            .record
            .settings
            .unwrap_or_default()
            .max_retained_superseded_bytes;
        let excess_bytes = superseded_bytes.saturating_sub(cap);
        if excess_bytes > 0
            && let Some(target_garbage_seq) =
                garbage_seq_to_fit_cap(transaction, cursor, excess_bytes)?
        {
            advance_window(
                transaction,
                cursor,
                WindowAdvanceTarget::GarbageSeq(target_garbage_seq),
            )?;
        }
    }
    prune_window_and_expire_segments(transaction, cursor, now_micros, &mut backlog)?;
    Ok(backlog)
}

/// Prunes what the window start passed and expires passed segments; marks `backlog` unfinished
/// when a bound stops a pass.
pub(crate) fn prune_passed_history(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
    backlog: &mut LocalCleanupBacklog,
) -> Result<(), ApplyError> {
    prune_garbage_before_window(transaction, cursor, backlog)?;
    prune_window_and_expire_segments(transaction, cursor, now_micros, backlog)
}

fn prune_garbage_before_window(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    backlog: &mut LocalCleanupBacklog,
) -> Result<(), ApplyError> {
    if prune_garbage(transaction, cursor, cursor.record.window_start_seq)? == GARBAGE_ROWS_PER_CALL
    {
        backlog.mark_unfinished();
    }
    Ok(())
}

fn prune_window_and_expire_segments(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
    backlog: &mut LocalCleanupBacklog,
) -> Result<(), ApplyError> {
    if prune_window_history(transaction, cursor)? == HISTORY_ROWS_PER_COMMAND {
        backlog.mark_unfinished();
    }
    if expire_passed_history_segments(transaction, cursor, now_micros)? {
        backlog.mark_unfinished();
    }
    Ok(())
}

fn garbage_seq_to_fit_cap(
    transaction: &FlowTransaction<'_>,
    cursor: &RunCursor,
    excess_bytes: u64,
) -> Result<Option<u64>, ApplyError> {
    let prefix = run_table_prefix(
        cursor.rows.flow_hash,
        cursor.rows.run_id,
        RunTable::ValueGarbage,
    );
    let (start, end) = prefix_range(&prefix);
    let garbage = transaction.scan_messages::<ValueGarbageRecord>(
        ColumnFamily::Flow,
        &start,
        &end,
        GARBAGE_ROWS_PER_CALL,
        "value garbage",
    )?;
    let mut collected_bytes = 0_u64;
    let mut target_seq = None;
    for (key, record) in garbage {
        let suffix = &key[prefix.len()..];
        if suffix.len() != 20 {
            return Err(ApplyError::InvariantViolation {
                detail: "value garbage key must have a 20-byte suffix".to_owned(),
            });
        }
        let garbage_seq = u64::from_be_bytes(
            suffix[..8]
                .try_into()
                .expect("garbage keys have an eight-byte sequence"),
        );
        if garbage_seq >= cursor.seq {
            break;
        }
        target_seq = Some(garbage_seq);
        collected_bytes = collected_bytes.saturating_add(record.size_bytes);
        if collected_bytes >= excess_bytes {
            break;
        }
    }
    Ok(target_seq)
}

/// Returns the smallest ValueGarbage key with lost-holder seq `seq`.
fn garbage_seq_key(prefix: &[u8], seq: u64) -> Vec<u8> {
    let mut key = prefix.to_vec();
    key.extend_from_slice(&seq.to_be_bytes());
    key
}
