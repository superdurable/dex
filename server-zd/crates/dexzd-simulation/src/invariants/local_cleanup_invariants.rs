// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Local cleanup invariants: the pending row matches the due time (H16); drained runs keep no
//! doable work (H17).

use dexzd_protocol::dexzd::v1::{HistorySegmentState, RunRecord};

use crate::invariants::{InvariantViolation, RunContext};
use crate::state_view::{FlowView, RunView};

/// Grace period of superseded values when the run's settings predate the field; an oracle
/// independent of apply.
const DEFAULT_SUPERSEDED_VALUE_GRACE_MICROS: i64 = 10 * 60 * 1_000_000;

/// Returns the run's grace period of superseded values: its recorded setting, or 10 minutes.
pub(crate) fn superseded_value_grace_micros(record: &RunRecord) -> i64 {
    let recorded = record
        .settings
        .unwrap_or_default()
        .superseded_value_grace_micros;
    if recorded > 0 {
        recorded
    } else {
        DEFAULT_SUPERSEDED_VALUE_GRACE_MICROS
    }
}

/// Every HistoryLocalCleanupPending index row of the Flow names a run that holds a due time (H16).
pub(crate) fn check_cleanup_index_runs_exist(
    flow_id: &str,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    for run_id in &view.history_local_cleanup_pending_runs {
        let has_due = view
            .runs
            .get(run_id)
            .and_then(|run| run.record.as_ref())
            .is_some_and(|record| record.history_local_cleanup_due_micros.is_some());
        if !has_due {
            return Err(InvariantViolation::new(
                "H16-local-cleanup-index",
                format!(
                    "Flow {flow_id} has a HistoryLocalCleanupPending row for run {run_id}, which \
                     holds no due time"
                ),
            ));
        }
    }
    Ok(())
}

/// The run's HistoryLocalCleanupPending row exists exactly while it holds a due time; purging
/// runs have neither (H16).
pub(crate) fn check_cleanup_index(
    context: &RunContext<'_>,
    run: &RunView,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    let Some(record) = run.record.as_ref() else {
        return Ok(());
    };
    let has_due = record.history_local_cleanup_due_micros.is_some();
    let has_row = view
        .history_local_cleanup_pending_runs
        .contains(context.run_id);
    if has_due != has_row || (record.is_purging && has_due) {
        return Err(InvariantViolation::new(
            "H16-local-cleanup-index",
            format!(
                "{context} holds due time {:?} with index row {has_row} while purging is {}",
                record.history_local_cleanup_due_micros, record.is_purging
            ),
        ));
    }
    Ok(())
}

/// Once no cleanup is due, the run keeps no local work a command could do (H17).
pub(crate) fn check_cleanup_drained(
    context: &RunContext<'_>,
    run: &RunView,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    let Some(record) = run.record.as_ref().filter(|record| !record.is_purging) else {
        return Ok(());
    };
    let drained = |detail: String| {
        Err(InvariantViolation::new(
            "H17-local-cleanup-drained",
            format!("{context} has no cleanup due but {detail}"),
        ))
    };
    if record.history_local_cleanup_due_micros.is_some() {
        return drained("still holds a due time".to_owned());
    }
    let genesis_seq = run.attempts.get(&1).map_or(1, |attempt| attempt.start_seq);
    let uploaded_through_seq = record.history_uploaded_through_seq;
    if view.is_run_history_offloaded(record) && uploaded_through_seq > 0 {
        // Window pruning may have deleted uploaded rows and values while offload was off.
        let rows_gone_through_seq = record
            .history_local_rows_deleted_through_seq
            .max(record.history_window_pruned_through_seq)
            .max(genesis_seq);
        if rows_gone_through_seq < uploaded_through_seq {
            return drained(format!(
                "deleted uploaded rows only through seq {rows_gone_through_seq}"
            ));
        }
        if record.history_values_offloaded_through_seq < uploaded_through_seq
            && record.window_start_seq <= uploaded_through_seq
        {
            return drained(format!(
                "offloaded values only through seq {}",
                record.history_values_offloaded_through_seq
            ));
        }
    }
    let has_sealed_segment = run
        .history_segments
        .values()
        .any(|segment| segment.state == HistorySegmentState::Sealed as i32);
    let is_fenced = view.is_run_history_offloaded(record)
        || (record.history_open_segment_first_seq != 0 && has_sealed_segment);
    let fence_seq = if is_fenced {
        uploaded_through_seq.saturating_add(1)
    } else {
        u64::MAX
    };
    if let Some((garbage_seq, value_id, _)) = run
        .value_garbage
        .iter()
        .find(|(garbage_seq, _, _)| *garbage_seq < record.window_start_seq.min(fence_seq))
    {
        return drained(format!(
            "keeps value {value_id:?} superseded at seq {garbage_seq} before the window start"
        ));
    }
    let keep_from_seq = window_keep_from_seq(run, record).min(fence_seq);
    if keep_from_seq > genesis_seq + 1
        && let Some(seq) = run
            .history_record_metadata
            .keys()
            .find(|seq| **seq > genesis_seq && **seq < keep_from_seq)
    {
        return drained(format!(
            "keeps history record {seq} before the kept seq {keep_from_seq}"
        ));
    }
    check_expiry_drained(run, record, genesis_seq).or_else(drained)
}

/// Returns the seq of the checkpoint that rebuilds the window start, or zero when the view
/// cannot name it.
fn window_keep_from_seq(run: &RunView, record: &RunRecord) -> u64 {
    let window_start_seq = record.window_start_seq;
    run.history_record_metadata
        .get(&window_start_seq)
        .and_then(|metadata| run.attempts.get(&metadata.attempt))
        .and_then(|attempt| {
            attempt
                .checkpoint_seqs
                .iter()
                .copied()
                .filter(|seq| *seq <= window_start_seq)
                .max()
        })
        .unwrap_or(0)
}

/// Requires that the first unexpired segment after genesis is not UPLOADED behind the frontier.
/// Returns what is left otherwise.
fn check_expiry_drained(run: &RunView, record: &RunRecord, genesis_seq: u64) -> Result<(), String> {
    if record.history_open_segment_first_seq == 0 {
        return Ok(());
    }
    let pruned_through_seq = record.history_window_pruned_through_seq;
    let first_seq = record.history_expired_through_seq.max(genesis_seq) + 1;
    // Expiry goes in seq order, so only the first segment after the expired ones can be due.
    let Some((_, segment)) = run.history_segments.range(first_seq..).next() else {
        return Ok(());
    };
    if segment.last_seq <= pruned_through_seq
        && segment.state == HistorySegmentState::Uploaded as i32
    {
        return Err(format!(
            "keeps segment {}..={} UPLOADED behind the pruning frontier {pruned_through_seq}",
            segment.first_seq, segment.last_seq
        ));
    }
    Ok(())
}
