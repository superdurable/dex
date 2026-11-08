// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Expiring the history segments that window pruning passed, and recording the deletion of their
//! objects (plan section 5.7).
//!
//! A segment expires once window pruning passed every seq it holds, so no read uses it again.
//! The segment that holds genesis never expires, since reads always show the run's first record.
//! Expiry deletes the segment's value list and the birth records of values that only its object
//! still held. Apply never reads the run's delete switch to decide expiry; the switch only keeps
//! the HistoryObjectDeletePending index row, which asks the owner to delete EXPIRED objects.
//!
//! The owner deletes objects oldest first, so the EXPIRED catalog rows are exactly the rows that
//! start after genesis and the run's deleted seq and end at or before its expired seq.

use dexzd_keyspace::row_keys::{
    history_object_delete_pending_index_key, history_segment_key, history_segment_value_prefix,
    prefix_range, value_row_key,
};
use dexzd_keyspace::{ColumnFamily, RunTable, ValueId};
use dexzd_protocol::dexzd::v1::{
    HistorySegmentObjectsDeletedCommand, HistorySegmentRecord, HistorySegmentState,
    HistorySegmentValueRecord, RunRecord, ValueBirthRecord,
};

use crate::command_handlers::command_replies::ok_reply;
use crate::command_handlers::run_cursor::RunCursor;
use crate::command_handlers::{CommandScope, HandlerOutput, HandlerResult};
use crate::history::genesis_seq;
use crate::history_offload::segment_seal::MAX_SEGMENT_SUPERSEDED_VALUES;
use crate::state_access::{FlowTransaction, RunRows};
use crate::values::reconcile_run_storage;
use crate::{ApplyEffect, ApplyError};

/// Bounds the catalog rows one command marks EXPIRED.
const MAX_EXPIRED_SEGMENTS_PER_COMMAND: usize = 256;

/// Marks EXPIRED, in seq order, the UPLOADED segments that start after genesis and end at or
/// before the run's window pruning frontier, and deletes their value lists and the birth records
/// only their objects held.
///
/// Each command expires at most 256 segments and lists at most 10,000 value rows; a segment that
/// lists more expires alone, since its seal wrote as many rows in one command. Later commands
/// continue. Asks the owner to delete the new EXPIRED objects when the run's delete switch is on.
/// `now_micros` is the command's proposal time.
///
/// Returns true when a per-command bound stopped the expiry, so a later command can continue.
pub(crate) fn expire_passed_history_segments(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    now_micros: i64,
) -> Result<bool, ApplyError> {
    let record = &cursor.record;
    let pruned_through_seq = record.history_window_pruned_through_seq;
    let mut expired_through_seq = record.history_expired_through_seq;
    if record.history_open_segment_first_seq == 0 || pruned_through_seq <= expired_through_seq {
        return Ok(false);
    }
    let rows = cursor.rows;
    let first_seq = expired_through_seq.max(genesis_seq(transaction, rows)?) + 1;
    if first_seq > pruned_through_seq {
        return Ok(false);
    }
    let candidates = transaction.scan_messages::<HistorySegmentRecord>(
        ColumnFamily::Flow,
        &history_segment_key(rows.flow_hash, rows.run_id, first_seq),
        &history_segment_key(
            rows.flow_hash,
            rows.run_id,
            pruned_through_seq.saturating_add(1),
        ),
        MAX_EXPIRED_SEGMENTS_PER_COMMAND,
        "history segment",
    )?;
    let mut listed_value_rows = 0_u64;
    let mut expired_count = 0;
    let mut is_bound_reached = false;
    for (key, segment) in candidates {
        if segment.last_seq > pruned_through_seq {
            break;
        }
        if segment.state == HistorySegmentState::Expired as i32 {
            return Err(ApplyError::InvariantViolation {
                detail: format!(
                    "history segment {}..={} is EXPIRED after expired seq {expired_through_seq}",
                    segment.first_seq, segment.last_seq
                ),
            });
        }
        // A segment sealed for migration may wait for its upload behind the pruning frontier.
        if segment.state != HistorySegmentState::Uploaded as i32 {
            break;
        }
        let with_segment = listed_value_rows.saturating_add(segment.superseded_value_count);
        if expired_count > 0 && with_segment > MAX_SEGMENT_SUPERSEDED_VALUES {
            is_bound_reached = true;
            break;
        }
        listed_value_rows = with_segment;
        delete_segment_value_list(transaction, rows, segment.first_seq)?;
        expired_through_seq = segment.last_seq;
        transaction.put_message(
            ColumnFamily::Flow,
            key,
            &HistorySegmentRecord {
                state: HistorySegmentState::Expired as i32,
                expired_at_micros: now_micros,
                ..segment
            },
        );
        expired_count += 1;
    }
    if expired_count == 0 {
        return Ok(false);
    }
    cursor.record.history_expired_through_seq = expired_through_seq;
    update_history_object_delete_pending(transaction, cursor, true)?;
    Ok(is_bound_reached || expired_count == MAX_EXPIRED_SEGMENTS_PER_COMMAND)
}

/// Deletes the value list of the segment that starts at `first_seq`, and the birth record of
/// each listed value whose content only the segment's object holds.
///
/// A birth record stays when its value is stored locally, when it is not offloaded, or when it
/// names another superseded seq. Time Travel or a revival restored such a value, or a later
/// segment lists it.
fn delete_segment_value_list(
    transaction: &mut FlowTransaction<'_>,
    rows: RunRows,
    first_seq: u64,
) -> Result<(), ApplyError> {
    let prefix = history_segment_value_prefix(rows.flow_hash, rows.run_id, first_seq);
    let (start, end) = prefix_range(&prefix);
    let listed = transaction.scan_messages::<HistorySegmentValueRecord>(
        ColumnFamily::Flow,
        &start,
        &end,
        usize::MAX,
        "history segment value",
    )?;
    for (key, listed_value) in listed {
        let value_id = key
            .strip_prefix(prefix.as_slice())
            .and_then(ValueId::from_key_suffix)
            .ok_or_else(|| ApplyError::InvariantViolation {
                detail: "history segment value key must end with a value ID".to_owned(),
            })?;
        let birth_key = value_row_key(rows.flow_hash, rows.run_id, RunTable::ValueBirth, value_id);
        let is_only_in_segment = transaction
            .get_message::<ValueBirthRecord>(ColumnFamily::Flow, &birth_key, "value birth")?
            .is_some_and(|birth| {
                birth.is_offloaded && birth.garbage_seq == listed_value.superseded_seq
            });
        let is_stored = transaction
            .get(
                ColumnFamily::Value,
                &value_row_key(rows.flow_hash, rows.run_id, RunTable::Value, value_id),
            )?
            .is_some();
        if is_only_in_segment && !is_stored {
            transaction.delete(ColumnFamily::Flow, birth_key);
        }
        transaction.delete(ColumnFamily::Flow, key);
    }
    Ok(())
}

/// Records that the owner deleted the objects of the named EXPIRED segments, oldest first.
///
/// Deletes the catalog row of each named segment while it is the run's oldest EXPIRED one, and
/// stops at the first name that is not. Replies OK without writes when no row is deleted, as for
/// a duplicate or stale command, and NOT_FOUND when the run is gone or purging. The command
/// records no history, takes no run seq, and is not bound to an attempt.
pub(crate) fn apply_history_segment_objects_deleted(
    transaction: &mut FlowTransaction<'_>,
    scope: &CommandScope<'_>,
    command: &HistorySegmentObjectsDeletedCommand,
) -> Result<HandlerResult, ApplyError> {
    let mut cursor = match RunCursor::open_without_seq(transaction, scope)? {
        Ok(cursor) => cursor,
        Err(reply) => return Ok(HandlerResult::Rejected(reply)),
    };
    let rows = cursor.rows;
    let expired_through_seq = cursor.record.history_expired_through_seq;
    let previous_deleted_through_seq = cursor.record.history_objects_deleted_through_seq;
    let mut deleted_through_seq = previous_deleted_through_seq;
    let genesis = genesis_seq(transaction, rows)?;
    for first_seq in &command.first_seqs {
        let oldest = transaction
            .scan_messages::<HistorySegmentRecord>(
                ColumnFamily::Flow,
                &history_segment_key(
                    rows.flow_hash,
                    rows.run_id,
                    deleted_through_seq.max(genesis) + 1,
                ),
                &history_segment_key(
                    rows.flow_hash,
                    rows.run_id,
                    expired_through_seq.saturating_add(1),
                ),
                1,
                "history segment",
            )?
            .into_iter()
            .next();
        let Some((key, segment)) = oldest else {
            break;
        };
        if segment.first_seq != *first_seq
            || segment.state != HistorySegmentState::Expired as i32
            || segment.last_seq > expired_through_seq
        {
            break;
        }
        transaction.delete(ColumnFamily::Flow, key);
        deleted_through_seq = segment.last_seq;
    }
    if deleted_through_seq == previous_deleted_through_seq {
        return Ok(HandlerResult::Rejected(ok_reply()));
    }
    cursor.record.history_objects_deleted_through_seq = deleted_through_seq;
    update_history_object_delete_pending(transaction, &mut cursor, false)?;
    reconcile_run_storage(transaction, &mut cursor)?;
    Ok(HandlerResult::Applied(HandlerOutput {
        reply: ok_reply(),
        effects: cursor.effects,
        run_id: None,
        replayed: None,
    }))
}

/// Writes or removes the run's HistoryObjectDeletePending index row, so that it exists exactly
/// while the run's delete switch is on and an EXPIRED segment keeps its object.
///
/// Emits [`ApplyEffect::HistorySegmentsExpired`] when the row exists and either it is new or
/// `has_new_expiry` says this command expired a segment.
pub(crate) fn update_history_object_delete_pending(
    transaction: &mut FlowTransaction<'_>,
    cursor: &mut RunCursor,
    has_new_expiry: bool,
) -> Result<(), ApplyError> {
    let record = &cursor.record;
    let is_wanted = is_expired_history_object_deletion_enabled(record)
        && record.history_expired_through_seq > record.history_objects_deleted_through_seq;
    let key = history_object_delete_pending_index_key(
        transaction.context().partition,
        cursor.rows.flow_hash,
        cursor.rows.run_id,
    );
    // A point get first, so a run that never expired a segment leaves no tombstone.
    let is_present = transaction
        .get(ColumnFamily::PartitionIndex, &key)?
        .is_some();
    match (is_wanted, is_present) {
        (true, false) => transaction.put(ColumnFamily::PartitionIndex, key, Vec::new()),
        (false, true) => transaction.delete(ColumnFamily::PartitionIndex, key),
        _ => {}
    }
    if is_wanted && (has_new_expiry || !is_present) {
        let run_id = cursor.run_id();
        cursor.add_effect(ApplyEffect::HistorySegmentsExpired { run_id });
    }
    Ok(())
}

/// Returns whether the owner deletes the objects of the run's expired history segments: the
/// run's FlowConfig override when set, or else the server default the run recorded at start.
pub fn is_expired_history_object_deletion_enabled(record: &RunRecord) -> bool {
    record
        .flow_config
        .as_ref()
        .and_then(|flow_config| flow_config.delete_expired_history_objects)
        .unwrap_or_else(|| {
            record
                .settings
                .unwrap_or_default()
                .delete_expired_history_objects
        })
}
