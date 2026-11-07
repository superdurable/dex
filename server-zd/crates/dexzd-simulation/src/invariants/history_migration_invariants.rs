// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Migration of history that runs recorded before they had segment state (plan section 5.17):
//! bounded work per command, and segment state that matches the migration.

use std::collections::BTreeMap;

use dexzd_apply::{ApplyEffect, ApplyOutcome};
use dexzd_keyspace::RunId;
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::RunRecord;

use crate::invariants::InvariantViolation;
use crate::state_view::{FlowView, RunView};

/// Migrations that started, caught up, and ended because offload turned off.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct HistoryMigrationCounts {
    /// Runs without segment state that started migrating.
    pub started: usize,
    /// Migrations whose last segment reached the command that sealed it.
    pub completed: usize,
    /// Migrations that ended because their partition stopped offloading.
    pub abandoned: usize,
}

impl HistoryMigrationCounts {
    /// Adds another count to this one.
    pub fn merge(&mut self, other: &Self) {
        self.started += other.started;
        self.completed += other.completed;
        self.abandoned += other.abandoned;
    }
}

/// Returns whether the command at `seq` must start or continue the run's migration: it took a
/// run seq while offload was on and left a run running whose history predates its segment state.
pub(crate) fn must_migrate(
    previous: &RunRecord,
    current: &RunRecord,
    view: &FlowView,
    seq: u64,
) -> bool {
    let has_older_history =
        previous.history_open_segment_first_seq == 0 || previous.history_migration_started_seq != 0;
    view.is_history_offload_enabled
        && has_older_history
        && current.next_run_seq == seq + 1
        && current.status == FlowStatus::Running as i32
        && !current.is_purging
}

/// Returns whether the command at `seq` started or continued the run's migration while offload
/// was on: it sealed one segment at the open segment's start, and the open segment moved right
/// after that segment, which holds two history records, or after the command once it caught up.
pub(crate) fn is_migration_step(
    previous: &RunRecord,
    current: &RunRecord,
    run: &RunView,
    view: &FlowView,
    seq: u64,
) -> bool {
    let (first_seq, first_event_id) = if previous.history_open_segment_first_seq == 0 {
        (
            run.attempts.get(&1).map_or(1, |attempt| attempt.start_seq),
            1,
        )
    } else if previous.history_migration_started_seq != 0 {
        (
            previous.history_open_segment_first_seq,
            previous.history_open_segment_first_event_id,
        )
    } else {
        return false;
    };
    let Some(segment) = run.history_segments.get(&first_seq) else {
        return false;
    };
    let has_caught_up = segment.last_seq == seq
        && current.history_migration_started_seq == 0
        && current.history_open_segment_first_seq == seq + 1
        && current.history_open_segment_first_event_id == current.next_event_id.max(1);
    let segment_records = run
        .history
        .iter()
        .filter(|(record_seq, _)| (segment.first_seq..=segment.last_seq).contains(record_seq))
        .count();
    let has_moved_after_segment = segment.last_seq < seq
        && segment_records >= 2
        && current.history_migration_started_seq != 0
        && current.history_open_segment_first_seq == segment.last_seq + 1
        && current.history_open_segment_first_event_id == segment.last_event_id + 1;
    view.is_history_offload_enabled
        && segment.first_event_id == first_event_id
        && current.history_open_segment_bytes == 0
        && current.history_open_segment_superseded_value_count == 0
        && (has_caught_up || has_moved_after_segment)
}

/// Requires at most one sealed segment per run and command (H11), and an open segment without
/// tracked bytes while a run migrates (H12). Returns the migrations the command moved.
pub(crate) fn check_history_migration(
    flow_id: &str,
    outcome: &ApplyOutcome,
    previous_records: &BTreeMap<RunId, RunRecord>,
    view: &FlowView,
) -> Result<HistoryMigrationCounts, InvariantViolation> {
    let mut seals = BTreeMap::<RunId, usize>::new();
    for effect in &outcome.effects {
        if let ApplyEffect::HistorySegmentSealed { run_id } = effect {
            *seals.entry(*run_id).or_default() += 1;
        }
    }
    if let Some((run_id, count)) = seals.iter().find(|(_, count)| **count > 1) {
        return Err(InvariantViolation::new(
            "H11-one-seal-per-command",
            format!("Flow {flow_id} run {run_id} sealed {count} segments in one command"),
        ));
    }
    let mut counts = HistoryMigrationCounts::default();
    for (run_id, previous) in previous_records {
        let Some(record) = view.runs.get(run_id).and_then(|run| run.record.as_ref()) else {
            continue;
        };
        if record.history_migration_started_seq != 0
            && (record.history_open_segment_bytes != 0
                || record.history_open_segment_superseded_value_count != 0)
        {
            return Err(InvariantViolation::new(
                "H12-migration-segment-state",
                format!("Flow {flow_id} run {run_id} tracks open segment bytes while migrating"),
            ));
        }
        let was_migrating = previous.history_migration_started_seq != 0;
        let is_migrating = record.history_migration_started_seq != 0;
        let has_started = previous.history_open_segment_first_seq == 0
            && record.history_open_segment_first_seq != 0;
        counts.started += usize::from(has_started);
        if (was_migrating || has_started) && !is_migrating {
            if view.is_history_offload_enabled {
                counts.completed += 1;
            } else {
                counts.abandoned += 1;
            }
        }
    }
    Ok(counts)
}
